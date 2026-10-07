# Reporting into Slack

Work the harness runs on its own is acceptable only while it is visible, and
today "visible" means a terminal somebody is sitting at. This is the same
account of the work, in a Slack workspace: **one thread per work item, one
message per milestone**, and every report an agent files carried through, as the
agent wrote it, at the severity it was filed under. Each thread's opening message
also carries **what its item is doing right now**, as a reaction, so the top of
the channel reads as a status board — see *The channel as a status board* below.

Who each message is from is worth being precise about. Each persona speaks under
its own name and face: the developer talks about the change it is making, the
reviewer about the change it is judging, the development manager about the
queue. What no persona did — a promotion, a merge, the operator's own switches —
arrives from **Yoyodyne** itself, because "the harness promoted this" is a real
account of a real act and no agent performs one. What an agent actually wrote —
a report, the argument in a proposal — is carried through word for word under
that agent's name.

Every one of those names says which product it is speaking for, taken from
`product.id`: **Development Manager (yoyodyne)**, **Yoyodyne (yoyodyne)**, and so
on for every speaker. An operator with a second product in development is reading
a second harness, and the name is the only thing a message carries that says
which one is talking.

None of that costs a model call. Every milestone is rendered from the record by
a fixed line per role, so the channel is deterministic and no model sits between
a fact and its reporting.

Nothing it posts is an act of its own. A separate process you start reads the
harness's own durable records and posts from them, so:

- Nothing waits on Slack. A workspace that is down, slow, or misconfigured
  changes nothing about any run — no wait, no failure, no parked work.
- Nothing is lost. The process catches up from its own cursors when it comes
  back, so an outage delays messages rather than dropping them. A process killed
  between posting a message and recording that it posted repeats that one
  message; the durable record is the authority and this is a view of it.
- Catching up does not flood the channel. Messages go at about one a second,
  which is what Slack keeps accepting indefinitely, and a backlog too deep to
  post one message at a time is summarized per thread instead of replayed. See
  *Coming back from a long gap* below.
- No run and no agent ever holds a Slack token. The tokens live in this one
  process's environment and nowhere else — never in `.yoyodyne`, never in a
  prompt, never in a run — and the harness builds every run's environment from
  an allowlist rather than handing down its own, so a token exported in the
  shell that started it stops at the harness. See *Where the tokens go* below.

Replies go the other way, and only for the people you say. An instruction in a
work item's thread, from somebody this project granted `direct-work`, is recorded
as a directive against that item and reaches the work exactly as one typed at a
terminal does; a question in the same thread is answered by the Lead Product Manager
there and recorded as nothing; anybody else the `operators` mapping names is
answered saying it was not acted on, and anybody it does not name is told once
that this app does not know them and who to reach out to instead. A project that
has named nobody is steered by nobody, which is what every workspace is until you
add yourself. See *Steering the work from a thread* below.

Setting this up takes about five minutes and needs a Slack workspace you can
install an app into.

**`yoyo setup` offers to do steps 4 and 5 for you**, which are the two that
happen on your own machine: it asks which channel to report into, writes the
configuration block, and then hands you the keychain's own prompt for each
token, under the same namespaced names step 5 describes. It will not overwrite a
pair that is already stored. Steps 1 to 3 are on Slack's screens and step 6
starts a process, so those stay here and stay yours; read them either way, since
what setup asks you to confirm is that you have done them.

## 1. Create the app from the checked-in manifest

The app is described by [`manifest.yaml`](manifest.yaml) beside this document.
Creating it from the manifest rather than by hand is what makes two workspaces
set up a month apart the same app, with the same scopes.

1. Go to <https://api.slack.com/apps> and choose **Create New App** → **From an
   app manifest**.
2. Pick the workspace.
3. Paste the contents of `docs/slack/manifest.yaml`, review the summary Slack
   shows you, and create it.

Slack will warn that Socket Mode needs additional setup in App Settings, and
that you can create the app first. That is not a refusal. Socket Mode is how
events reach a machine behind NAT, and it needs an app-level token; there is
no app yet to hang one on. Create it. The token is the next step.

If Create itself is blocked — some workspaces treat the warning as a hard
stop — paste the manifest with `socket_mode_enabled` set to `false` and the
`event_subscriptions` block removed, create the app, then continue from
step 2 and turn Socket Mode on there. Events without a request URL require
Socket Mode, so they cannot go in until it is actually on. Once it is, paste
the original manifest back in under *App Manifest*.

**The one thing the manifest cannot carry is the icon.** Slack takes an app's
image through its own upload and not through a manifest, so this is the single
part of the profile you set by hand: *Basic Information* → *Display Information*
→ **App icon**, and upload [`docs/slack/app-icon-v1.png`](app-icon-v1.png),
which is checked in beside this document and beside the role faces in
[`avatars/`](avatars) at the 1024px Slack asks for. It is the same engraved gear
the harness speaks under in the channel, so the app and its messages read as one
thing. Skipping it costs nothing but the grey placeholder Slack assigns every
app that has not been given a face.

The manifest says what each scope is for. The two that read messages —
`channels:history` and `groups:history` — are what carries a message in the
channel back to your machine: a thread reply, which is how the harness is
steered, and an @-mention of the app, which is answered and — for anything that
is not a question about the standing — taken to the Lead Product Manager. An operator
who would rather not do either from Slack can delete those two scopes and the two
`message.*` events beside them: everything else in this document still works, and
nothing typed in the channel ever arrives.

`im:write` is used for two classes of message and only those two, sent directly
to whoever step 4 grants `direct-work`. The first is **the harness reporting
itself degraded**: a session choosing work from a build the harness has moved
well past, the harness having started nothing at all while work was ready,
[the provider holding every role](../reporting.md#the-provider-holding-every-role)
with nothing configured to fail over to,
[an item that sat claimed with nothing working on it](../reporting.md#an-item-claimed-with-nothing-working-on-it)
until the harness gave it back, and a line that has stopped over ready work —
which is the one of them that is *asked* rather than reported, with the answers
numbered and your reply in the thread as the decision; see *Deciding a stopped
line from a direct message* below. Beside them, and needing no `im:write`
at all, [a provider nobody is logged into or nobody can reach](../reporting.md#a-provider-nobody-can-reach)
is said once in the channel tagged to those same members by id, and once more
when it answers again. The second is **advisory-once** — a
fact said exactly once and never repeated: a value the project's template has
improved that this project never edited, and
[a finding that needs your hand](../reporting.md#a-finding-that-needs-your-hand)
— a report the Lead Product Manager handled as yours, one filed at critical
severity, a stopped run the development manager escalated to you, or the batch
of recommendations an owning role argued on a recurring pass over the changes
proposed to its documents — which is
tagged to you by member id as well, since it is yours to act on. The stale build, the
released claim, the improvement, and each finding are sent once rather than
repeated, and at
most one improvement message goes per reading however many the reading found;
the hold is sent when it is first seen and again with each heartbeat once it has
stood past six hours, because it is the one state a person ends early; the
stall is sent again with every heartbeat it stands, tagged to those members by
id in the channel as well, because a line that has stopped for reasons nobody
can name is the one state that gets louder rather than quieter; and the stopped
line is asked once per state per person. A brake hold
handed to you — by the development manager, or by the harness once its
summons-and-probe loop has gone round its configured number of times — is
tagged the same way each hour, and sent directly once it has stood two hours;
the brake's trip is sent directly the once, the moment it is recorded, naming
the runs it counted and `yoyo release`; and either escalation — hers or the
harness's — is also sent directly the once, the moment it is recorded. Removing the scope costs those direct
messages and nothing else: the stale-build message, the hold, the released
claim, the stopped line, the improvement, and each finding are in the channel
either way, the
stall and the brake hold are still tagged there, and the stall is in the durable
record `yoyo status` reads back.

`im:history` and the `message.im` event beside it are what carry what you say in
a direct message with the app back: your reply to an ask, and a message there
that @-mentions the app, which is answered exactly as one in the channel is.
Remove them and you are still asked; what you answer never arrives, and nothing
is recorded. The message events are scoped to conversations this app is in, so
what it can read is the channel it was invited to and the direct messages it
has with people — never anybody else's.

## 2. Install it and take the two tokens

1. *Basic Information* → **App-Level Tokens** → **Generate Token and Scopes**.
   Give it any name, add the `connections:write` scope, and generate it. It
   starts `xapp-`. This is the token that opens the Socket Mode connection;
   without it the app has no way to reach your machine. If *Socket Mode* in
   the left nav is still off after this, turn it on — that is the additional
   setup Slack asked for when you created the app.
2. **Install to workspace** on the app's *Basic Information* page, and approve
   the scopes.
3. *OAuth & Permissions* → copy the **Bot User OAuth Token**. It starts `xoxb-`.

Keep both somewhere your shell can read them and nothing else can. They are
credentials for posting into your workspace.

## 3. Invite the app to the channel

Create or pick the channel the threads should be opened in, and invite the app
to it:

```
/invite @yoyodyne
```

An app that has not been invited is refused by Slack with `not_in_channel`. The
sink says that once and then waits it out quietly, retrying every few minutes
without repeating itself: what clears it is you inviting the app, and a log line
every fifteen seconds until you do would not make it clear any sooner. Nothing is
lost while it stands — the cursors do not advance past a message that was never
posted — and the sink says so again when the workspace starts accepting messages.

Take the channel's id while you are there: **channel name → About → the id at
the bottom**, which looks like `C0123456789`. A name works too, but an id
survives somebody renaming the channel.

## 4. Tell the project where to report

In your project's `.yoyodyne/config.yaml`:

```yaml
slack:
  enabled: true
  channel: C0123456789
```

**It is already in the file, commented out.** `yoyo init` scaffolds this block,
and an `operators` example beside it, under a paragraph pointing back at this
document — so the work here is deleting the leading `# ` from each line and
putting your own channel id in. A configuration written before that existed has
no such block; adding one is the same three lines.

A project that enables reporting without naming a channel is refused when the
configuration loads, before any work is claimed. A project that says nothing
about Slack reports nothing, which is every project until it opts in.

Each speaker has a face as well as a name, and the face is yours to change —
including to a custom emoji this workspace already has:

```yaml
slack:
  enabled: true
  channel: C0123456789
  avatars:
    developer: ":ship-it:"
    harness: https://example.com/faces/yoyodyne.png
```

Keys are roles, or `harness` for what no persona did; values are an emoji
shortcode or the https URL of an image. Leave a speaker out to keep the picture
the harness ships. Both shapes work with the scopes the manifest already asked
for, so neither costs a reinstall. **The names are not configurable** — only the
picture is: who speaks is a claim about who did the work, and that stays the
harness's to make. The product each name carries is not a choice either; it is
read from `product.id`.
[`docs/configuration.md`](../configuration.md#avatars) has the whole of it.

Who may steer the harness from a thread is not part of this block. It comes from
the top-level `operators` mapping, which is where the project says which humans
it recognizes — and a human is bound there by all of their identifiers rather
than by their Slack one:

```yaml
operators:
  your-name:
    git_email: you@example.com
    slack_member_id: U01234567   # your profile → "Copy member ID"
    grants:
      - direct-work
```

The allow-list is then derived: the humans granted `direct-work` who have bound
a member id, and nobody else. Until somebody is on it, every reply is answered
saying it was not acted on, which is what a workspace gets by default. The member
id lives in the configuration rather than in the environment because it is
identity rather than a secret.

The mapping is also who this app *knows*, which is the wider list: everybody with
an entry here, whatever you granted them. Somebody with no entry at all is told
once per thread that this app does not know them, and the names in this mapping
are who they are told to reach out to — so an entry with no grants is worth
writing for a colleague who should be recognized without being able to steer
anything. A mapping that names nobody says this to nobody.
[`docs/configuration.md`](../configuration.md#operators) has the rest of the
mapping, including the other grant and the namespaces you can bind.

> **Moved:** this used to be `operators` *inside* the `slack` block. It is not
> accepted there any more, and a configuration that still has it is refused when
> it loads, with a message naming the entry to write instead.

## 5. Store the two tokens under this project's names

They go in the sink process's environment and nowhere else, and they are read
into it from somewhere only its own launch looks. On macOS that is the keychain,
which keeps them encrypted at rest:

```sh
# once, with <product id> as the `product.id` in .yoyodyne/config.yaml:
security add-generic-password -s yoyo-slack-bot.<product id> -a yoyo -w
security add-generic-password -s yoyo-slack-app.<product id> -a yoyo -w
```

`-w` with no value makes the keychain prompt for the token, so it never reaches
your shell history. Elsewhere, a `chmod 600` file this project's launch sources
does the same job in plaintext at rest — write the two `export` lines into
`~/.config/yoyo/<product id>/slack.env`.

**The names carry the product deliberately.** A generic pair is
indistinguishable between projects, and indistinguishable is how a sink ends up
posting one project's work into another project's channel: running more than one
harness on a machine is the ordinary case, not the exotic one, and under a shared
name every check of the form "a Slack token exists" passes for all of them while
at most one of them is right. Under these names, `yoyo doctor` can ask whether
*this project's* secrets are stored.

## 6. Start the sink

On macOS, the harness does this for you:

```sh
yoyo slack ensure
```

It starts a sink only if nothing is reporting for this product, reads this
project's own keychain items into that one process, and returns either way — so
it is what an unattended pass can run every few minutes as well as what you type
once. It prints what it did, in one of four ways: a sink already running, a sink
it started and the pid it started as, the stored items it could not read, or
reporting turned off for this project so there is no sink to run. Nothing it
prints is a token. It fails only on the third, which is the one outcome somebody
has to do something about; a project reporting nowhere is healthy and says so.

**The product's supervisor makes this start for you.** Enable the Slack service
in the configuration's [`services`](../configuration.md#services) section and
[`yoyo start`](../operations.md#starting-the-product-and-stopping-it) starts
the sink with the rest of the product — the same lease-checked start as above,
from the same keychain items — and starts it again whenever it dies, within the
supervisor's bounds; a sink that cannot start because the items are not stored
is shown as degraded by `yoyo status`, with this step's own words for what is
missing. Typing `yoyo slack ensure` yourself is still right for a product you
have not started, and a pass of your own may still call it: with a sink already
running it does nothing.

Whether a sink is running is asked of **this product's lease**, which is the
same lease the sink itself takes and one per product. That is what makes it
right on a machine running more than one harness: a `pgrep` for `yoyo slack`
matches the sibling project's sink, so a pass built on one would decide this
product's sink is running when it is the other product's, and this project would
report nothing indefinitely. The tokens come from this product's names for the
same reason. Run it in each product; the products do not see each other.

The sink it starts is in a session of its own, so it stays up when the pass, the
terminal, or the job that started it goes away, and it says what it is doing in
`sink.log` beside that product's own sink state. `--json` is the same account
for something that reads it rather than somebody.

Underneath, that is exactly the launcher below, which is what to write where
there is no keychain to read — or where you want the sink in front of you rather
than behind you.

The launcher reads this project's pair into exactly one process:

```sh
#!/bin/sh
# ~/bin/yoyo-slack-<product id> — the assignments are on the exec line, so the
# tokens exist only in the sink's environment and never in your shell's.
SLACK_BOT_TOKEN="$(security find-generic-password -s yoyo-slack-bot.<product id> -a yoyo -w)" \
SLACK_APP_TOKEN="$(security find-generic-password -s yoyo-slack-app.<product id> -a yoyo -w)" \
YOYO_SLACK_SECRET_NAMESPACE=<product id> \
exec yoyo slack "$@"
```

With the environment file instead, the subshell does the same:

```sh
(set -a; . ~/.config/yoyo/<product id>/slack.env; YOYO_SLACK_SECRET_NAMESPACE=<product id> exec yoyo slack)
```

`YOYO_SLACK_SECRET_NAMESPACE` is not a credential and is not read as one. It is
how the sink records whose secrets it was launched with, so something other than
the sink can tell one that is merely running from one that is running for this
project. Leave it out and reporting works exactly as before; what is lost is
anything being able to notice when it is wrong.

It prints the workspace and channel it connected to, and then stays open until
you stop it with Ctrl-C. Leave it running in a terminal, a `tmux` window, or
whatever you use to keep a long-running process around.

To check the setup without leaving anything running, ask for a single pass:

```sh
yoyo slack --once
```

That posts whatever is due and exits.

**It reports what happens from the first time you ever start it.** A product
with two hundred runs behind it does not get two hundred threads on the day
somebody turns reporting on: work that was already over is left in the records,
where `yoyo status` and `yoyo reports` read it. Work still in flight is caught up
on in full, and the first pass prints the moment it is reporting from.

That moment is written down once, on that first pass, and every later start
reads the same one back rather than taking a new one. It matters more than it
sounds: a sink that started its history at each launch would treat everything
that happened while it was stopped as work from before it cared, and the
`critical` filed overnight is exactly the message that would go missing. Because
the moment is fixed, downtime is a gap the sink reads across — a report filed
while it was stopped, and a run that began and ended while it was stopped, are
both posted when it comes back.

If you do want to start the history over — a channel you have wiped, a product
you are re-pointing at a new workspace — stop the sink and delete
`projects/<product id>/state/slack/cursors.json` under your state root. That is
`$YOYODYNE_STATE_HOME` if you set it, the `state_root` in your `machine.yaml` if
you set that, `$XDG_STATE_HOME/yoyodyne` if you set that, and otherwise `~/.yoyodyne` — or
the earlier builds' default, `~/Library/Application Support/Yoyodyne/state` on
macOS or `~/.local/state/yoyodyne` elsewhere, while your state is still there. The sink takes a new moment on its next pass
and says which one. Leave `threads.json` beside it alone unless you also want new
threads, and `steers.json` — which is what it remembers about replies that
steered the work — alone either way: a directive settled before the new moment is
read past on its age, so starting the cursors over does not answer you again for
everything you ever steered. `refusals.json` beside them is the threads it has
already told somebody it does not know them in; deleting it says that sentence
once more in each of those threads, which is the whole of what it costs.

**Do not run two.** One sink per product: two of them hold separate thread maps,
so the second opens its own threads and posts everything twice. The second to
start is refused with a message saying so, and the refusal clears by itself when
the first one exits.

## 7. Ask whether it is actually reporting

```sh
yoyo doctor
```

A sink fails quietly — a channel that says nothing and a harness with nothing to
report look identical — so this is the verb that asks the questions the channel
cannot answer: are this project's secrets stored under the names above, is a sink
actually holding this product's lease or is there only the record of one that
died, is the sink that is running **this build**, and was it launched with **this
project's** secrets. Every finding it makes carries the command that fixes it.

The build question is the one that catches an installation that was working and
stopped. The sink is a long-lived process started from a binary that keeps moving
underneath it, so the build that is reporting and the build that is installed
drift apart with no event between them: nothing fails, nothing is logged, and the
milestones added since it started are simply never posted. In the channel that
reads as a quiet week.

Everything doctor says about reporting comes back as a **warning**, and it still
exits 0. That is the same rule as everywhere else here: reporting is an
observation and never a gate, so a sink you never started, a workspace that is
down, and a token nobody stored all leave a machine that runs work exactly as it
was. Do not wire `yoyo doctor`'s exit status up as a check on whether reporting
is healthy — read the findings, which name every one of these in full and carry
the command that ends it.

## What it posts

Each thread is headed by the item it is about — its identifier and what the item
is called, as `yoyodyne-ifd.118 — Slack thread headers carry the item's title` —
so a channel scrolls as a list of subjects rather than a list of identifiers. The
title comes from the durable record whatever opened the thread was read from.
Where that record carried none — an item whose first appearance in the channel is
its priority changing, which is every item admitted before you had a channel —
the tracker is asked what the item is called, once, as the thread is opened. A
tracker that will not answer costs that header its title and nothing else: the
thread opens either way. Threads that are already open stay exactly as they are.

The header is the only place a work item's identifier appears. **Every message
inside a thread names the work in words** — `Re-arm the dropped-merge check`, not
`yoyodyne-ifd.102.7` — because the header above it already carries the
identifier, and an item nothing has ever recorded a name for is said as *this
item* rather than as its slug. That holds for every item a message mentions: a
decomposition lands in the thread of the item it created, and says that item was
cut out of a larger one rather than naming that larger item's slug, because the
record keeps its identifier and nothing that says what it is called. Which item
that was is in the tracker and in the durable record. **No conversation identifier is posted at all**: the
record still carries it and `yoyo status` still reads it, but it is not something
anybody reading a channel does anything with. **No directive identifier is posted
either**: what you get for a reply you typed is the reply read back to you and
what it does to the work, and a slug in an acknowledgment is the one thing in it
you would have to go and resolve. The record still carries it, and
`yoyo directive list` is where you read it. What a message does carry — in words
where the sentence needs it, and in italics under it — is what you would follow
rather than look up: the run, an exchange, the pull request. And **the reasoning
a role wrote into the
tracker is one sentence here**, with the message saying that the rest of it is in
the item's record: the argument is written for somebody weighing the decision in
the tracker, and a paragraph of it under a one-line fact is what makes a channel
go unread.

Into the work item's thread, as they happen:

- the item arriving in the backlog: **admitted, with the goal it serves**, or
  decomposed out of the item above it, said by the role that did it
- work you approved from a proposal, admitted with the goal it was proposed under
- a goal recorded on an item already in the queue, and an item's priority changed
- the run starting, **carrying the reason that work item was selected**
- the checks passing or failing — passing said with what the check stage spent
  of its bound, so a slow stage is visible before the run the bound stops
- a change refused before its checks for touching a
  [protected path](../configuration.md#protected-paths-in-a-developers-change)
  the item does not grant, said by the developer as a `warning` for the reason a
  failing check is one — a repair round is being spent. It names the refused
  paths, what the item grants, and the `protected-path grant:` line that would
  admit them, so a reader watching a repair round happen sees why without
  opening the item's notes. It is said once per refusal, as the round it bought:
  the same paths refused again on the next attempt are a second round spent, and
  are said again; a refusal that finds the budget already spent buys no round,
  and it is the blocker line below that names its paths
- the reviewer's verdict, approved or sent back for repairs
- the promotion onto the target branch
- the pull request, a merge the forge queued, and the merge itself
- a merge that is not going to happen — the forge refused it, usually because
  the remote target moved under the run, or gave up on one it had queued — said
  as a `warning`, because nobody chose it and nothing else in the record says it:
  the change is promoted, the thread reads as landed, and what is left is a
  publication waiting on a person
- what the landing checks made of the commit the run landed, once the run is
  over: a green landing is an ordinary fact in the thread; a red one is a
  `warning` naming the check that failed and the item the harness filed for it,
  because it is the target branch broken by a change every gate passed, which
  nobody finds out about on their own; and one the checks could not run over
  is a `warning` too, because a landing nobody verified reads as green to
  anybody who was not told. See
  [where the whole suite runs](../configuration.md#where-the-whole-suite-runs)
- the run waiting — an exhausted usage limit, an overloaded provider, an
  operator hold, an unresolved directive — and the run carrying on afterwards. A
  run waiting out an exhausted usage limit is said as a `warning`, because it
  means hours in which nothing will happen for a reason nobody chose; the other
  three are ordinary facts, since an overload lifts in seconds and a hold or a
  directive is waiting on the person reading the channel
- a turn served by a model other than the one it asked for, said as a `note`:
  nothing stopped, which is the point of it, and it is said because an agent
  answering on a different model is a change to what the work was produced by. It
  names the model that would not serve, the one serving instead, and — in the
  cause — which of the two reasons it was: the configured model's capacity window
  being closed, or a pinned version this provider has not got. It is said once
  per window rather than again while one stands, where a window is the provider's
  own reset time or, for a refusal the provider never dated and for every missing
  version, `execution.usage_limit_unknown_reset_pause`. See
  [serving a turn from a permitted alternate model](../configuration.md#serving-a-turn-from-a-permitted-alternate-model)
  and [pinning an agent to a model version](../configuration.md#pinning-an-agent-to-a-model-version)
- the blocker that stopped a run, if one did, said at the severity whoever moves
  next and whatever stopped it warrant — `critical` only where that is you: a
  cause somebody has to fix on the machine, such as a target branch that
  diverged from the remote's, a credential the remote refused, or a primary
  checkout holding uncommitted state. A stoppage the environment caused that a
  role or the harness moves next — a lost race for the target, a replay the
  harness killed, a tracker or forge that did not answer, a usage window — is a
  `note`, and stays in the item's thread. One the work caused — findings nobody
  repaired, a check that kept failing, paths the item never granted, a replay
  that conflicted — is a `warning`, at the channel level, because it is the
  development manager's real decision
- the run ending any other way — failed, cancelled, timed out — said in that word
  rather than in one word for all of them. A run the harness could not carry and
  one it stopped on time are `warning`s, because nobody chose either; a
  cancellation is an ordinary fact, since somebody did. Every one of these lines
  and the blocker line above also say what remains of the change — work
  preserved, work removed, or no artifacts recorded — because the attempt being
  over says nothing about whether the work is
- every report an agent filed against that item, as the agent wrote it
- every change an agent proposed to a document it does not own, with the
  argument it made for it
- the item having sat claimed with nothing working on it until the harness gave
  it back — a run killed with its claim still standing — said once as a
  `warning`, shown at the top of the channel as well as in the thread, and sent
  to whoever you grant `direct-work` too, because the item had left the ready
  queue and nothing else in the record says the line was idle behind it. What
  follows it is the run that starts again, which says so itself. See
  [claims with nothing working on them](../operations.md#claims-with-nothing-working-on-them)

At the top level of the channel, unthreaded, goes what is about the whole line
rather than any one item: the operator holding and releasing intake, the
operator holding and lifting all harness activity, and a block of tracker actions
the harness refused whole, said as a `warning` with the role that asked, how many
actions it asked for, and the refusal itself, because none of them happened and
the role that asked for them believed they had. That one is a `warning` rather
than a `critical` because the harness is about to try to repair it: it hands the
refusal back within the same message, or wakes that role's own conversation once
where the message had no round left, and the role re-issues the actions itself.
The second line is what you get when that did not work — a second block refused
with the first still unanswered, or the round it was handed back in or the woken
turn answering without asking for any tracker action at all — and the message
says which of those it was. It is a
`critical`, because the actions are still lost, the harness has stopped trying,
and nothing further is scheduled.
A line of one of the harness's own logs that the sink cannot read goes here as
well — a write a crash tore, or a record written by a build newer than the
sink's — said once, as a `warning` naming the log and the line, with the
decoder's own words. The sink keeps that line's place and carries on to the
records after it, so one torn write costs one message rather than every message
behind it for as long as the line stands; what the line held is not said, and
the file is yours to look at.
Burying those in one item's thread would misfile them. A channel catching up on a backlog too deep to replay posts
its digest here too, for the same reason: it stands for messages that were going
to appear at this level, and one line saying how many is what both you and the
workspace want out of four hundred.

Two things look like they belong in that list and do not, and both are the same
case: no item, so no thread, so saying them at all would mean saying them at the
top of the channel — which puts the messages with the least attached to them in
the loudest place there is.

**Proposed work you turned down** is the first. Nothing was created, so there is
no item and there never will be one, and what a message would tell you is what
you have just decided. It is in the durable record and `yoyo` reads it back.

**A report an agent filed against no work item** is the second. It is kept in the
report store, which is where every report is kept, and reaches you the way the
rest of them do: in the pile the Lead Product Manager is handed each turn, worst
first, until somebody records what became of it. A `critical` one is the
exception and is posted, because something already wrong is what you asked to be
told wherever it is.

That list is what is *addressed* to the channel rather than everything that
appears in it: a thread reply is shown there as well when what it says is
important or needs you, which is what the rule below does.

A provider refusing the harness for want of capacity goes there too, wherever it
happened. The harness asks a provider for work in three places, and each one
accounts for a refusal:

- **inside a run**, for the developer attempt or the review — the run parks, and
  the park is what is posted, in that item's thread
- **a conversation turn** with any role, which has no run to park
- **an independent `yoyo review`**, which uses the same reviewer with no run
  around it

The last two record the refusal themselves, and it is posted here as a `warning`
naming what was stopped and, when the provider quotes one, when the limit lifts.
Without it an exhausted limit reaches only whoever typed the command, and hours
of silence with a known cause look exactly like a quiet queue.

A watching `yoyo work` session is not a fourth place. It reads the tracker and
starts runs, and makes no provider call of its own, so a limit it meets is met by
a run it started and said by that run parking. That the list above is the whole
list is checked rather than asserted:
`TestEveryProviderInvocationAccountsForAnExhaustedLimit` sweeps the tree for
every provider invocation and fails on one that has no account of what an
exhausted limit does to it — so teaching selection, or anything else, to ask a
provider something arrives with a failing test rather than with a process that
goes quiet and says nothing.

What a watching `yoyo work` session is doing goes to its durable log rather than
to the channel, with one exception. Opening, a poll that started nothing,
resuming, ending, and stopping to be restarted onto a newer build are the poll-by-
poll narration of a process that spends most of its life saying nothing, and they
were 473 of the 2,250 posts an operator survey counted. `yoyo status` reads them
back, and nothing posts them. The exception is **a held intake braking the
session**, which is a `warning` at the channel level because it needs somebody:
the line has stopped and stays stopped until intake is released.

What that leaves is a session that quietly went away, and two messages below
cover it from the same log: the hourly line names a session that has stopped as
soon as there is work it would have started, and the stall alarm covers the case
where it died having recorded nothing at all. Over an empty queue neither says
anything, which is the healthy quiet this is deliberately silent through.

**And one thing is a state rather than an event.** Everything above is said once,
when it happens, which is right for a narrative and wrong for a night: intake
held at 00:02 and a session that stopped at its budget both said so, correctly,
and then nothing said anything for ten hours that could be told from a healthy
quiet queue or a dead sink. So a line that is **choosing nothing while work is
ready** says so again while it stands — every `--heartbeat`, an hour by default —
naming what stopped it, how long that has been true, how much ready work a
run could have been started for behind it, and how many promotions are waiting on
the forge to publish them:

> Nothing is being chosen on this product: intake is held — the harness held
> intake after runs kept blocking, for 10 hours now, with 4 items ready to pull
> and one promotion awaiting the forge.

Five states count: the operator holding all harness activity, a held intake
(whoever held it), a watch session that has found nothing it can start, a watch
session whose last read of the harness's store failed and is being retried, and
no watch session running at all. A retried read is never said as a session that
found nothing: the queue was not read, and the move is the harness's, which reads
it again until the store answers or the session gives up and stops. Each
closes on who it is waiting on, in the words `yoyo status` puts on its
attention line — for a held intake, the hold's own:
yours for one you placed, the development manager's or the harness's for one
the brake is working — naming which summons-and-probe cycle it is on and at
what cycle the harness stops asking — and yours once it is escalated, by her or
by the harness at that bound. That last one is the one state here that gets
louder as it stands: a brake hold that waits on you is tagged to you by member
id every hour, a `warning` while it is young and `critical` and sent to you
directly once it has stood two hours, until intake is released. It stops the
moment the state clears, and says nothing about the clearing — the release, the
session opening, or the run it starts says that itself.

The count of promotions is the second thing that makes it speak, and it is there
because a **dropped merge** is said once, as it happens. A reader who was away
for that message has nothing else that would ever tell them: the change is
promoted, the thread reads as landed, and the pull request sits on the forge. So
the count comes back with the line while the publication stands, and a line with
nothing ready at all says so as long as there is one. It is the same derivation
the attention line of `yoyo status` names each of them on, so what the count says
is waiting is what the terminal lists, with who it is waiting on against each; and
it stops counting a publication the moment the forge records the merge, which
[`yoyo reconcile`](../operations.md#recovering-interrupted-runs) writes onto the
record whether the harness made the merge or somebody made it by hand.

It is otherwise deliberately narrow about when it speaks. A run in flight is not
a stalled line, so nothing is said while work is visibly moving. A product nobody
has ever watched is not one either: running items by name is a queue you are
choosing to keep, not a harness waiting on you. And **an idle line with nothing
ready and nothing waiting on the forge stays completely silent**, which is the
whole point — silence has to keep meaning nothing to do, so that the times it
does not are worth reading. Turning it off is not offered, because what that buys
is silence that means waiting on you; how often is `--heartbeat`.

**And one thing is the absence of a state.** All four of those are read from
something a process wrote down, which works only while that process is alive to
write it: a watch session that crashes writes no stop, and one that wedges goes on
recording that it is watching. So the harness watches for nothing having
happened at all — no run started for ten minutes, work a run could be started
for, and no hold, full machine, run in flight or provider usage window to account
for it. That is recorded against the product by the two commands that take the
reading — [`yoyo work --watch`](../work.md#letting-the-harness-choose-the-work) as
it polls, and [`yoyo reconcile`](../operations.md#recovering-interrupted-runs) on
every sweep — rather than by this process, and the sink sends the record to
whoever you grant `direct-work` as a direct message, tagged to them by member id
in the channel — and again every `--heartbeat` while the stall stands, never
once per check, as a warning while it is young and critical once nothing has
started for two hours. The sweep is run for you by the product's supervisor, on
its [maintenance pass](../operations.md#the-supervisors-maintenance-pass) every
`services.maintenance.every`; you schedule `yoyo reconcile` yourself only where
that part is off or no supervisor is running:

> Nothing at all has started on this product for 1 hour, with 47 items ready to
> pull: 33 of the 47 admitted items are awaiting carry-out of decisions already
> recorded. The session choosing work last recorded idle at
> 2026-09-06T02:05:00Z, and has said nothing since. Next: the harness's — the
> decisions are recorded, and what is outstanding is the harness acting on them.

The two counts are two readings and are not the same number twice: the tracker
calls 47 items ready, and the last poll's own account of those 47 says a third of
them are waiting on somebody. That gap is the ordinary case rather than an error
— the tracker does not know what the harness is holding — and seeing both is what
tells a queue nothing will pull from a queue nothing is pulling.

**The cause is the last poll's own, rather than this message's.** A session that
starts nothing records what it passed over and why — awaiting a decision,
awaiting carry-out of one already recorded, parked, carried in a conversation,
sequenced behind a run, waiting out the provider's usage window — and this reads
that account rather than working out a second one from the silence. The first two
are named apart because they are two different people to go to, and the message
above is the case that says why: the development manager had decided all
thirty-three of those stoppages, and a sentence that sent the operator to her
cost days. The two used to be derived separately and disagreed: on
2026-09-06 the alarm said nothing accounted for an hour of quiet while the
session's own idle line, in the same log, held the whole accounting.

**An account a start overtook names nothing.** Where no poll left an account — a
session that stopped cleanly, or one that never idled — the message says the
cause is something the record does not name, and the move is the operator's. The
same is true where something started after the last poll and the line then went
quiet: the queue has not been read since it moved, so that account is not stated
as the present cause and the message points at the chooser instead.

**A named cause is not evidence that the chooser is alive.** A session that died
while idle polled after the last thing that started, so its account survives that
bound and is named — and it is the right answer about the queue, because those
items really are held. What says whether the process is still there is the
chooser's last word in the same message: "last recorded idle at
2026-09-06T02:05:00Z, and has said nothing since" is a session to look at
whatever the queue is holding.

The chooser's last word is the other thing to act on: a session whose last word
was `stopped` wants starting, and one still claiming to be watching wants killing
first. When it clears the record closes and the channel hears nothing — the run
that started says that itself — and
[`yoyo status`](../operations.md#when-nothing-happened-at-all) reads the whole
history back afterwards, which is the only place it exists.

**Ten minutes is a default, and `--stall-after` on either of those commands is
where it is changed.** The threshold is on them rather than on this process
because everything on this page is optional: while the sink was the only thing
taking this reading, a product that never turned Slack on recorded no stalls at
all, and those are the installations least able to notice a harness that has
stopped. So the harness's own loop takes the reading as it polls, the sweep takes
it for the case that loop cannot see — itself being dead — and this reports what
they recorded. `yoyo slack --stall-after` is still accepted so a launcher passing
it starts a sink; it decides nothing, and the sink says so when it is given one.

**The sink says where the reading is taken, every time it starts.** Two lines, on
stdout, whatever the flags were: that stalls are noticed by those two commands and
not here, and that a machine running neither records none. It is unconditional
because losing the watchdog looks like nothing at all from a channel — a product
with no stalls and a product nobody is checking read the same — and an
installation that had one before this changed must not lose it quietly.

A watching session holds to the threshold exactly, since it takes the reading
itself. Where the session is the thing that died, how long a stopped harness can
be quiet is the threshold *and* how often the sweep runs, so set those two
together — a sweep every half hour makes a ten-minute threshold mean half an hour.
Set the threshold wider on a machine whose gaps between runs are legitimately
long; there is no way to set it to nothing, because what that buys is silence that
means the harness is dead.

**Nothing on this path may ask a model anything.** Every watcher the harness has
that does — the development manager's sweep, any role turn — pauses with the
provider's usage window, so a watchdog built on one sleeps through exactly the
silence it is there to notice. Both halves are plain Go reading durable files,
which is what keeps them working through a window; and noticing is all either
does. Restarting whatever died is the session's own bounded exit and the
supervisor that starts it, not this.

**The provider's usage window is not a stall, and says so instead.** When a run
comes back parked on an exhausted usage limit, the watch session records entering
that window and the time the provider said it lifts, and every surface reads that
as the accounting it is. The channel gets one note per window rather than the
alarm above, and nobody is messaged directly:

> Paused on the provider's usage window until 13:43Z. Nothing has been chosen on
> this product for 30 minutes; nothing has stopped and nothing is waiting on
> anybody, and the harness asks again when the window lifts.

**The cause is the first words.** That is the acceptance the operator wrote for
this rather than a house style, and today's page failed exactly it: the alarm led
and the cause was left to archaeology. It holds for every message this state
produces — the note above, the refusal `yoyo status` prints against work it is
holding back, and the banner `yoyo status` opens with while the window stands.

That distinction was bought: on 2026-09-05 a session waited a window out from
12:13Z to 13:43Z, the alarm fired at half an hour saying nothing accounted for
it, and somebody was paged for a machine doing exactly what the provider had
told it to. The window accounts for the quiet only until the time the provider
named — a session still choosing nothing after that is a stall again, which is
what keeps this from being a way to switch the watchdog off. It is also why the
window is not one of the four heartbeat states above: it has this message, and a
second one saying the same silence with the cause second would be the thing the
acceptance rules out.

A run in flight quiets it as well, and only while that run is
still moving. That distinction is the difference between this working and not:
a killed run leaves a record saying it is in flight until
[`yoyo reconcile`](../operations.md#recovering-interrupted-runs) settles it, so
reading "in flight" as "working" would silence the watchdog for exactly the
crash it is watching for. What separates the two is the run's own record — a
working run stamps every provider event onto it as it goes — so a run whose
record has not moved for an hour stops accounting for the quiet. The hour is
well clear of anything a live run does: an invocation that emits nothing for
five minutes is stopped as stalled, and the slowest legitimate wait, a provider
usage limit, probes every half hour.

Reading what is ready costs one local tracker (`bd`) read, and never on the path
of any run. One thing here wants that number now — the waiting line above, which
asks at most once a `--heartbeat` — because the watchdog's own read went with the
watchdog: a watching session takes it at most once per `--stall-after`, the sweep
takes it at most once per sweep, and both only where nothing else already accounts
for the quiet. A held line, a full machine, a run still moving and a standing
provider usage window each answer without it and cost nothing. A drained queue
does not: nothing but the tracker can say the queue is drained, so a perfectly
healthy idle product pays that one read per reading, and what bounds the cost is
the threshold and the sweep's cadence rather than anything inside the reading. A
tracker the sink cannot read — no `bd` on the machine it runs on, say — costs that
one message: the sink says so in its own log and asks again at the next interval,
rather than guessing a number in either direction, and the sweep reports the same
refusal the same way.

What a cadence costs is promptness rather than the stall: a stall is noticed at
the first reading after the threshold has passed — the next poll of a watching
session, or the next sweep where the session is what died — and it closes at the
first reading after it clears. The moment recorded against it is when the harness
last started something rather than when anybody noticed, so the event says how
long nothing happened whatever the noticing cost.

**And one thing is not about the work at all.** A project generated from a
built-in template records what that template supplied, and
[`yoyo config drift`](../configuration.md#extending-a-built-in-bundle) reports
every value the template has improved since that this project never edited.
`doctor` and `config validate` say the same thing as an aside — but all three are
commands somebody runs, and a harness left running for a fortnight runs none of
them, so a fix the template has since made to a persona sits unheard. So the sink
says it: one message per reading that finds something new, in the channel and as
a direct message to whoever you grant `direct-work`. A reading that finds one
improvement says it with both its values:

> builtin:v1 has improved agents.developer.model, a value this project has not
> edited: it was "sonnet" and is "opus" now. Nothing has changed and nothing is
> waiting on anybody: `yoyo config drift` shows agents.developer.model beside
> everything else the template moved, and it is adopted by hand or not at all.

A reading that finds several — the first one on a project many template
revisions behind — says them together, counted and the first few named, rather
than sending one message each:

> builtin:v1 has improved 12 values this project has not edited:
> agents.developer.model, agents.reviewer.model, checks, execution.poll,
> execution.heartbeat, and 7 more. Nothing has changed and nothing is waiting on
> anybody: `yoyo config drift` shows what each one was and is, and each is
> adopted by hand or not at all.

Each improvement is said **once and never again** — marked in the sink's own
durable state as it is sent, one mark per value whether it was said alone or
among several, so a restart says nothing about one it already sent and a later
reading that finds one more says that one alone. A template that improves the
same setting again later is a second improvement and is said again; nothing is
ever adopted for you, and the message says outright that the next move is
nobody's. The comparison costs one reading of your configuration per
`--heartbeat` rather than one per poll.

The queue changing comes from the conversations you hold with the Lead Product
Manager and the development manager, read from the same durable records `yoyo
status` reads. A conversation's log is mostly the turn itself, and none of that
is posted: what reaches the channel is the few points where the backlog actually
moved. A conversation you replace with a new one stops being read from, so a sink
that was down while you replaced one may miss the tail of what the old one did —
the durable records still have it.

Severity is said in words rather than only in colour: a `critical` says
"Critical" and a `warning` says "Warning", so a client that renders no emoji
still shows them for what they are. An ordinary fact carries no marker, because a
label on everything is a label that means nothing.

**Where a message is seen is decided by what it is, not by how loud it is.**
Slack's main channel view hides thread replies, and what is shown there anyway is
what is **important** — something that matters is broken or has materially
changed — or what **needs your action or decision**. Everything else stays in its
item's thread, where the narrative is, and some of it is not posted at all: it
lands in the durable record and reaches you in the regular summaries built from
there. A message that is neither important nor asking for anything does not go to
the top of the channel, whatever severity it was filed at.

Concretely: a held intake, a braked line, a parked run, a provider that ran out of
capacity, a merge the forge will not make, a directive that paused work, a stall,
a stale session, a claim the harness gave back, a refused block of tracker
actions, a line of a harness log the sink could not read, a change an agent
proposed to a document it does not own, a cap the
development manager crossed on his own authority, and every turn of an ask
exchange are all at the channel level — the released claim because the line was
idle behind it for as long as it stood; the crossing because it is a veto by
reading, applying as it is recorded and yours to undo only if you see it; the
last because an exchange is a
question waiting on you, and a
question shown only inside a thread while its answer is shown at the top would be
the two ends of one ask surfaced opposite ways round. A run starting, checks
passing, a review approving, a promotion, a publication, a merge completing, the
backlog moving and a filed report are in the item's thread. What a watch session does poll by poll — started, idle, resumed,
stopped, redeploying — is not posted anywhere: the watch log holds every one of
them, `yoyo status` reads it back, and the two states you have to act on are said
by the braked line and the hourly waiting line instead.

There is one promotion over all of that, and it is severity doing the job it is
for: a `critical` — something already wrong that will cost somebody — is shown at
the channel level whatever its kind. Severity is importance rather than
actionability, so if something important is broken you are told; what it no
longer does is push every `warning` to the top, which is what a survey of 2,250
posts found had drowned the forty that actually needed somebody under some 1,700
that did not.

Each transition is said once. A thread is a narrative rather than an event log
scrolling sideways, so a restart does not repeat what it already said — how far
each record has been read is written down as each message goes out and survives
the process. What can honestly happen twice is said twice: a check that fails
again, differently, after a repair attempt is its own message, and so is a run
that waits out a second usage limit. The heartbeat above is the one thing here
that is not a transition, and it repeats on purpose: what somebody coming back in
the morning needs is not that the line stopped, which was said at midnight, but
that it is still stopped now.

One thing to expect is not a thread at all. **Ask exchanges are designed and not
built.** Every persona has words ready for a turn of one and for one closing,
including one closing unresolved at its round cap, but nothing produces them yet,
so no `exchange:` thread is ever opened. When that work lands it adds messages to
this channel and changes nothing about your setup.

## The channel as a status board

Messages say what happened; they cannot say what is happening. Each transition is
said once — correctly, because a thread is a narrative — so the state an item is
in right now is somewhere inside a thread rather than on it, and finding which of
twelve threads needs you means opening twelve threads.

So the message each thread hangs from carries one reaction, and it is the item's
current status. There are four, and they are the whole vocabulary:

| Mark | Status | What it means |
| --- | --- | --- |
| :hammer_and_wrench: | working | A run is in flight on the item — developing, at the checks, repairing, being integrated. |
| :eyes: | in review | The change is with the reviewer. |
| :octagonal_sign: | blocked | The run stopped: failed, cancelled, timed out, or waiting on a provider, on your hold, or on a directive nobody has resolved. |
| :white_check_mark: | completed | The run finished and succeeded. |

The mark is replaced as the record moves and the one that has stopped being true
comes off, so a scan of the channel's top level answers what is working, what is
blocked, and what landed without a thread being opened. Nothing else is ever
added to an opener: **a status is about the item and a severity is about one
message**, and they never share a symbol, so a reader never has to work out which
of the two a mark is talking about. Anyone reacting to a thread themselves is
untouched — the sink only ever adds and removes those four.

Three things are worth knowing about it:

- **It is read from the item's latest run.** An item whose second attempt is in
  flight reads as working, whatever the first attempt did; what the first one did
  is in the thread. An item with no run yet — a thread opened by the backlog
  moving, or by a report — carries no mark at all.
- **It is reconciled rather than posted.** The status is worked out afresh from
  the durable records on every pass, so a run that finished with nothing left to
  say still stops reading as working. A change takes the other three marks off
  before putting the true one on — the opener wears at most one of them, so the
  rest of those calls hit nothing — which is what makes a sink killed mid-change
  settle instead of leaving a mark behind: whatever the opener is wearing when
  the next change comes, it is one of the four and it is not the one being set,
  so it comes off.
- **It is never a gate, and not even a message.** A workspace that refuses the
  reaction costs the channel its status board and not one message: the sink says
  so once in its own log and keeps posting.

That last one is the case to expect if you installed the app before this existed:
the marks need the `reactions:write` scope, which the checked-in manifest asks
for. **An app installed from an older manifest keeps reporting and silently marks
nothing** until you reinstall it from *OAuth & Permissions*.

## Steering the work from a thread

An instruction in a work item's thread is a **directive** against that item: the
same record [`yoyo directive record`](../conversation.md#directives-and-the-work-they-pause)
writes, kept where every run of that item reads it before it starts, before it
resumes, and before it puts a change through the gate. Nothing about the channel
is a second kind of instruction — it is one more way into the record you already
have, which is why steering from a phone is as enforceable as steering from the
terminal the harness is running on.

Reply with what you want done, and it is recorded as an operational directive:
it applies from that moment, with nothing waiting on it.

```text
prefer the smaller change here — don't refactor the store as well
```

**A question is answered, and never recorded.** Every reply is read for what it
is for before anything is written down, and a reply that ends with a question
mark goes to the Lead Product Manager instead of into the record — the same durable
conversation `yoyo chat` holds, which is what *Asking the app directly* below
reaches from the top of the channel. What you see is a one-line receipt saying
it was heard as a question, then the Lead Product Manager's answer in the same
thread, under the Lead Product Manager's own name, tagged to you. The receipt does
not quote your question back: a receipt that answers a question about a phrase
by repeating the phrase is the defect this exists to end. It costs a turn, the
way any message to the Lead Product Manager does, and it is held to the same
`direct-work` grant.

```text
What does 'applies from now on' mean?
Did you restart?
```

The reading is a stated rule rather than a classifier, and it is the directive
record's own — `yoyo directive list` reads its entries by the same rule — so the
channel and the terminal cannot disagree about which sentence is which. A reply
that ends with a question mark is a question. A reply that asks something and
then goes on, or that opens like a question and ends with no mark — *what does
this mean*, *is this done*, *can you make it smaller* — is neither, and gets one
line back asking you to end it with a question mark or say it as an instruction;
nothing is recorded and nothing is asked on your behalf, because either guess is
a real cost. Everything else is an instruction and is recorded exactly as it
always was. A reply that opens with `ambiguous:` or `artifact:` is not read this
way at all: it says what it is for, and `ambiguous: which of the two did you
mean?` is your own question, recorded as one on purpose and pausing the work
until you answer it.

The rule cannot stop work, and the two kinds that do are still yours to state.
It used to be that the record took everything: on 2026-08-30 the operator asked
in a thread what a phrase in a receipt meant, the reply was recorded as a
standing instruction, and the acknowledgment repeated the phrase he was asking
about. A question in the directive record is a directive nobody gave, and
enforceability presumes there are none — which is why `yoyo directive list` and
`/directives` now mark any entry that still applies and reads as a question, so
the ones recorded before this can be found and withdrawn.

Two kinds pause the work instead, and **you say which**, because a classifier
deciding that a sentence stops work is worse than one that never stops it. Open
the reply with the word, and say what is unresolved — a pause nobody can name a
reason for is a pause nobody can lift:

```text
ambiguous: which of the two publishing behaviours did you mean
artifact: slack-reporting-design whether product threads may carry directives
```

`ambiguous:` is one nobody can act on without deciding something you did not.
`artifact: <name> <what has to be decided>` is one that rewrites a governed
document — the brief, a goal, a design — so work derived from it waits until the
change is decided. Either one stops the item at its next gate without cancelling
anything: the run keeps its claim, its branch, and its worktree.

Lift it by saying how it was settled, in the thread it was said in. **Nothing
here needs an identifier**: the reply settles whatever that item is waiting on,
which is the item the thread is about.

```text
resolve the second one, and say so in the design
```

**What that item is waiting on can be wider than that item.** A directive
recorded against no work item — `yoyo directive record` without `--scope` — is
holding every item in the product, so it is holding this one too, and settling it
from here lifts it everywhere it was stopping work. The answer says so when that
is what happened: *It was holding every item in this product rather than this one
alone, so it is lifted for all of them.* One recorded against several items says
how many it was holding, the same way.

Two cases are refused instead of settled, in a sentence in the same thread: an
item waiting on nothing, and an item waiting on more than one thing — where the
refusal names them by what each is waiting on and how far each one reaches, since
picking one for you would be the channel deciding which of your pauses to lift. To
settle one of several, name it. Any prefix of the identifier that names exactly
one will do, and
`yoyo directive list` at the terminal is where you read it, because no message
posted here carries one:

```text
resolve directive-3f2a the second one, and say so in the design
```

`yoyo directive resolve` settles it at the terminal without a reply at all.

Open the reply with `@developer`, `@reviewer`, `@architect`,
`@development-manager`, or `@product-manager` to record who you told. That is
attribution rather than routing — the record reaches every run of the item
whichever role it names — and a reply that mentions nobody is the Lead Product
Manager's.

Every reply is answered in its own thread, **tagging you**, with the directive as
recorded and what it does to the work — *Recorded, for the Lead Product Manager:
prefer the smaller change here — it applies from now on, and nothing waits on
it.* — or with why nothing was recorded. What that answer says is the whole of
what happened at that moment: there is no other
confirmation, and a reply that stopped work is shown at the top of the channel as
well, because work stopping is what somebody who has opened no threads most needs
to see.

Your own message then carries where that directive stands, as a reaction, so
scrolling back through what you said says which of your replies is still open and
which has been answered without any of them being read:

| Mark | What it means |
| --- | --- |
| :thinking_face: | Recorded, and not settled yet. It goes on as the reply arrives and stays while the directive stands. |
| :white_check_mark: | The directive is answered — carried out, decided, resolved, or withdrawn. It lands when the outcome is said in the thread, not when the directive was written down. |
| :no_entry_sign: | Nothing was recorded. The thread says why. |

The mark is about the directive rather than about the harness having read you:
recording one is not disposing of it, so a directive nobody has settled keeps
saying so however long that takes. Those three are the whole vocabulary, they are
only ever on a reply, and the four status marks are only ever on a thread's
opener, so no message carries both.

**What later becomes of it is said in the same thread, tagging you** — and that
is the moment the mark on your message moves. A directive you asked for from a
thread is remembered against that thread, so when the record says it was settled
— by you at a terminal, in a conversation, by anybody — the settlement and what
it was are said where you asked rather than only where it was typed. A directive
recorded at a terminal has no thread and nobody to tag, so nothing is said about
it here.

An ordinary reply records an operational directive, which pauses nothing and so
has nothing to resolve. What settles one of those is somebody carrying it out,
and the case that reaches most replies is the Lead Product Manager admitting the work
you asked for: the item it admits names your directive, and the directive's own
record is told which item it became. That is what the thread then says back to
you — the identifier of the work, not just that you were heard — so a reply that
turned into a work item stops wearing :thinking_face: at the moment there is
something to go and read.

The check mark says you have an answer, not that your instruction has lapsed.
An operational directive applies from the moment it is recorded and stays
there; carrying it out records what it produced and withdraws nothing, so it is
still listed by `yoyo directive list` as active, with what it became under it.
What ends one is you withdrawing it — `yoyo directive withdraw --by <who>
--reason <why> <id>`, or `/withdraw` in a conversation — which takes it out of
force without deleting it. There is no way to do that from a thread, but a
directive you asked for from a thread and later withdrew is answered there all
the same: one line in that thread, tagging you, saying it was taken back and why
— *The operator took that back, so it no longer applies; what was directed while
it stood stays on the record: recorded in error* — in the voice of the role whose
conversation you withdrew it in, or the harness's own where you did it at a
terminal. A reply still wearing :thinking_face: moves to :white_check_mark: at
that moment, because there is now an answer to read; one that already had the
check mark keeps it. The listing is where a withdrawn one reads as withdrawn.

Seven things are refused, visibly:

- **a reply from somebody without `direct-work`**, or with the grant but no
  `slack_member_id` bound. The list defaults to empty, so a workspace steers
  nothing until you add yourself in step 4. A question is held to the same
  grant, since it reaches the Lead Product Manager.
- **a reply that reads as either a question or an instruction**, which is asked
  back in one line rather than guessed at. End it with a question mark to ask,
  or say it as an instruction to record it.
- **a question with nobody to answer it** — a sink started without the Lead Product
  Manager's conversation, or the Lead Product Manager already mid-turn. Nothing is
  recorded either way, and the refusal says to ask at `yoyo chat`, or to say it
  again once that turn lands.
- **a reply from somebody the `operators` mapping does not name at all**, which
  is refused differently: they are told once in that thread that this app does
  not know them and who to reach out to instead, and everything they say in it
  after that is written to the sink's log and answered with nothing. See *Somebody
  this project does not know* below.
- **a stated kind that says nothing unresolved** — `ambiguous:` on its own, or
  `artifact:` without a document and what to decide about it.
- **a reply in a thread that is not a work item's.** A directive from a thread is
  scoped to the item the thread is about, and one recorded against no item would
  pause the whole product. `yoyo directive record --scope` is how a wider one is
  recorded.
- **a `resolve` reply naming nothing, where the item is waiting on nothing or on
  more than one thing.** Nothing is settled either way, and the thread says which
  of the two it was.

Everything else in a channel is left entirely alone: a message that is not in one
of these threads, a thread this sink never opened, and anything the app itself
posted — with two exceptions: *Asking the app directly* below, which answers and
never records, and a reply in a direct message the sink opened to ask you
something, which is the next section. The last is not a nicety: the sink's own
messages arrive back on the same connection, and reading one as an instruction
would be the harness directing itself.

`yoyo directive list` shows what is recorded whichever way it arrived, and
`yoyo directive resolve` settles one from the terminal. The two surfaces are the
same record.

## Deciding a stopped line from a direct message

The hourly waiting line above is the one state the sink asks you about rather
than reports. The same moment it first says a line has stopped over ready work
in the channel, it opens a direct message with each person granted `direct-work`
with a bound Slack member id — the same allow-list a thread reply is acted on
by, and nobody else in `operators` — one conversation each, and puts the
decision to them:

> **Nothing is being started, and it is waiting on you.** intake is held, and
> the harness's own brake placed it after runs kept blocking. 4 admitted items
> are ready to pull behind it — reply in this thread to decide.

The context is threaded under that line, with the answers numbered:

> Stopped by: intake is held, and the harness's own brake placed it after runs kept blocking
> Since: 2026-08-30T02:02:00Z
> Ready to pull: 4
>
> Reply with a number:
> 1. release intake so admitted work can be chosen again
> 2. keep intake held; let what is running finish and choose nothing new

What stopped the line is the read model's own sentence — the same one `yoyo
status` prints against the queue and the channel line says — so the ask and the
channel can never disagree about whether the line is stopped or what by. What
the sink adds is the answers, which are its own: one pair per state the
heartbeat repeats (everything held, intake held, a dispatch waiting out the
tracker, a session that cannot read the harness's store, the watch session idle,
no session running), and every state also takes an answer in your own words.

**Your reply in that thread is the decision.** There is no button and nothing to
type at a terminal: a number takes the option it names, and anything else is
recorded in your own words. It does have to be *in the thread* — a conversation
can hold several asks, and the thread is what says which one you are answering.
Answer in the conversation instead, which is what tapping a phone notification
opens, and you are told so: nothing is recorded, and you are never left thinking
you decided something you did not. While the ask is still the live one you are
pointed at it — *the ask about … is above*; once its state has cleared you are
told that instead, and that nothing is waiting on a decision from you now,
because a pointer at a thread about a line that is already moving would send you
to decide something that is over. Where the sink cannot read which state is
standing, it says that rather than either: the ask is named, and so is the fact
that whether it still stands could not be read. A message there that @-mentions
the app is not a misplaced decision — it is answered the way *Asking the app
directly* below describes. Either way a recorded decision lands as one
operational directive in the same record `yoyo directive record` writes and
every run consults — unscoped, because what you were asked about is the whole
line rather than one item — and it carries what was asked, which option you
took, and the words you typed, so somebody reading it weeks later can
reconstruct the decision. The thread answers you by name with which option was
taken and the sentence it stood for — and, as everywhere else here, no
identifier: `yoyo directive list` is where the record is read back.

The two halves of that are worth being plain about. Nothing acts on your answer
by itself: choosing "release intake" records that you decided to, and does not
release intake — the switches stay yours, and a chat message that could throw
them would be a second thing deciding what the harness does. And you are asked
once per state per person, however long it stands: the channel repeats it every
`--heartbeat`, which is a room you scroll, while a direct message repeated hourly
is what gets an app muted.

Each operator is asked separately rather than in one conversation, because a
decision addressed to a room is one everybody can reasonably assume somebody else
is making. Somebody without `direct-work` replying in one of these threads is
told so and nothing is recorded, the same way a channel reply from them is. And
a line said for a promotion waiting on the forge alone, with nothing ready to
pull, asks nobody: nothing is choosing nothing over ready work, so there is no
decision to put, and the channel line carries the count. A
reply into an ask whose state has since cleared is still recorded: a decision
made late is still a decision. And if the workspace refuses the direct message —
an app somebody has never opened, a workspace that does not let its apps message
people — the channel still says the line is stopped, the pass finishes normally,
and the ask is tried again at the next heartbeat.

This needs `im:write` and `im:history` and the `message.im` event, which the
checked-in manifest asks for. An app installed from an older manifest has none of
them: reporting is unaffected and nobody is ever asked, which shows up as the
`the stopped line could not be put to <member>` line in the sink's own log.
Reinstalling from the current manifest is what fixes it.

## Asking the app directly

**@-mention the app and you always get an answer.** Anywhere the sink can see
you — the top of the channel, a thread it never opened, or a direct message with
the app outside the thread of an ask — a message that names the app is answered
where you said it, in a reply hanging from your own message and tagging you. This is the one thing the sink says outside its own threads, and
it exists because the alternative was silence: a question at the top of the
channel had no handler at all, which reads exactly like a sink that has died.

This is for the humans your `operators` mapping names, whatever you granted
them. Somebody with no entry there gets the sentence in *Somebody this project
does not know* below instead of an answer.

Ask where things stand and you get the four lines in full — the same four
[`yoyo status`](../operations.md) prints, entry by entry, read from the same place
so a channel and a terminal cannot answer one question two ways. You asked, so
you get the detail; the hourly line nobody asked for carries the same four lines
with their queues counted rather than listed:

```text
@yoyodyne what is running?
@yoyodyne status
```

Words like `status`, `sitrep`, `what is running`, `what are you doing`, and
`where do things stand` all ask for it. It costs no turn and no money: the
standing is a derivation the harness already has.

**Everything else goes to the Lead Product Manager**, and comes back in the Lead Product
Manager's own name and face, in the thread you asked in:

```text
@yoyodyne what is missing from the brief?
@yoyodyne put the installer epic ahead of the dashboard
```

With one exception: **the `/` commands are not carried out from here.** `/work`,
`/stop`, `/backlog`, `/refresh` and the rest are your own authority carried out
by the harness rather than anything the Lead Product Manager can do, so a message that
opens with a slash is answered with where to type it — `yoyo chat`, or `yoyo` at
the terminal — and nothing is said to the Lead Product Manager. It costs no turn.
Slack's composer takes a slash you type at the start of a line, but a mention
comes first here, so `@yoyodyne /backlog` reaches the app as a command and is
refused as one rather than read out to the Lead Product Manager as a sentence.

It is the [same conversation](../conversation.md) `yoyo chat` holds, not a second
one this channel keeps. There is one durable conversation per agent and both are
clients of it, so a question you asked at your terminal is answered from your
phone and carried back: the provider session is resumed rather than restarted,
the turns accumulate on one record, and a proposal made in one client is decided
in the other — typing `y` in the channel approves what `yoyo chat` put to you,
exactly as typing it at the terminal would.

Three things follow from it being one conversation whose turns are exclusive.

**The channel holds it only while it is answering.** It opens the conversation
when you ask something and releases it as soon as the answer is in hand — the
same span a `yoyo chat` holds it for, which puts the conversation down whenever
it is waiting at its prompt. So a `yoyo chat` you leave open is never locked out
for longer than one turn, and never locks the channel out at all while it is
idle. If the Lead Product Manager is mid-turn with another client when your message
arrives — your terminal answering, or the harness delivering something — the
thread says so rather than failing quietly, nothing is said, and you say it again
once that turn lands. Closing a `yoyo chat` changes nothing here: what holds the
conversation is a turn, and a turn ends on its own.

**One at a time.** A second message that arrives while a turn is running is told
so in its own thread rather than queued or dropped.

**The wait is bounded.** A turn gets ten minutes and then the thread is told what
happened — the wait running out, the conversation mid-turn elsewhere, or the
provider's own reason, which for an exhausted usage limit is that limit in its
own words. Nothing hangs silently; a run steered from a conversation can wait on
capacity for hours, and that is a choice you make at a terminal rather than
something a channel does to you.

It is a bound on what *you* wait for rather than a stop signal the turn obeys, so
a turn the channel gave up on may still be working — a provider sleeping out a
usage limit does not hear a cancellation. **Until that turn lands it holds the
conversation, and the thread says so.** A message you send from the channel in
the meantime is answered with the Lead Product Manager being busy. `yoyo chat` is not
refused and does not show you a turn that is still being written: it queues
behind the turn, prints that another process is mid-turn with the Lead Product
Manager and that it is waiting, and continues the same conversation from
wherever that turn got to once it has landed. `yoyo agent list` says whether the
Lead Product Manager is still mid-turn without waiting on it, so that is the reading
to take before deciding whether the wait is worth sitting through.

**Who may.** Talking to the Lead Product Manager admits work, reorders the queue, and
spends your money, so it is held to the same `direct-work` grant a thread reply
is. Somebody your mapping names and granted nothing still gets the four lines —
those are already in this channel — and is told which grant they are missing for
anything else.

Two things it is not. **A mention still records no directive**: what it does is
speak to the Lead Product Manager, and a question at the top of the channel — where
there is no item to scope a directive to — is answered rather than refused.
Nothing said to the app is lost either: the sink's own log gets a line for every
message addressed to it, with what was asked in it, written before the answer
goes out, so a question the workspace would not let it answer still leaves a
record that somebody asked. And **the standing discloses nothing**: those four
lines are already posted to this channel by the heartbeat, so that answer tells a
reader nothing the channel was not already telling them.

What the Lead Product Manager does while it answers — work it admits, an item it
reprioritizes, a question it puts to another role — reaches this channel the way
it always has, through the ordinary reporting of the conversation's own record.
The answer in your thread is the prose; the rest is the channel's normal traffic
rather than a second account of it.

A message that does not name the app is still left entirely alone, which is what
keeps a reporting channel a reporting channel rather than a participant in
everybody's conversation.

## Somebody this project does not know

A channel has other people in it. Somebody whose Slack member id is bound to
nobody in your [`operators`](../configuration.md#operators) mapping is told so,
once, in the words:

```text
I don't know you. Please reach out to mason-bryant if you need something.
```

The names are the entries in your mapping, said the way the mapping files them
rather than as Slack mentions — so telling one person who to ask does not notify
everybody in it. It is said in the two places the app is spoken to at all: a
thread this sink opened, and a message that @-mentions the app anywhere it can
see.

**Once per thread, and no more.** The next thing the same person says in that
thread is written to the sink's own log — with what they said, so you can see
who wanted what — and answered with nothing. An app that can be made to talk by
repeating yourself is one you would turn off, and this channel is a report on
your work rather than a conversation with the workspace.

Three things follow from that, worth knowing before somebody asks you about
them:

- **The mark, not the sentence, is what a second attempt gets.** Nothing is
  posted, so a person watching the channel sees one refusal in a thread however
  many times it was tried.
- **It survives a restart.** The thread is remembered in the sink's own state
  beside the cursors, so a sink restarted overnight does not greet the same
  person again in the morning.
- **A mapping that names nobody says this to nobody.** There is no boundary to
  be outside of, and nobody to name as a contact, so a workspace that has not
  filled the mapping in behaves exactly as it did before: every mention is
  answered, and a reply that would steer is refused for the grant it is missing.

If a colleague is getting this and should not be, they need an entry in
`operators` — a `slack_member_id` and nothing else is enough to be recognized,
and granting them `direct-work` is the separate decision that lets them steer.

## Coming back from a long gap

A sink that was off overnight comes back to everything the harness recorded while
it was down. Two things shape how that reaches the channel.

**It is posted at the rate Slack sustains** — roughly one message a second. Slack
does not delay an application that posts faster than it tolerates: it suppresses
the overflow, tells the application `due to a high volume of activity, we are not
displaying some messages sent by this application`, and those messages are hidden
for good rather than late. Pacing is what keeps a catch-up late instead of
invisible, and it is why a twelve-hour gap takes a few minutes to appear rather
than arriving at once.

**A deep backlog is digested per thread.** When one pass has more than about a
minute of messages in it, everything older than the last half hour is collapsed
into a single line in each item's thread — how many events accumulated, over what
span, and the reminder that the durable record holds every one of them. Four
things are never collapsed: anything in a backlog shallow enough to post in full,
anything from the last half hour, anything **critical**, which is always said
in its own words, and anything sent to a person directly, which is said once
and marked as said — so a finding that needs your hand reaches you after an
outage exactly as it would have without one. `yoyo status` and `yoyo reports`
read the full record from the command line whenever the digest is not enough.

## Limits worth knowing

- **The thread map is per machine.** Two people running their own harnesses
  against one shared repository would each open their own threads for the same
  work item, because neither can see the other's map. That is team coordination
  and it is not solved here: one sink per product per workspace is the supported
  shape.
- **A message that is too long is truncated**, with a marker naming the durable
  record that holds the whole of it. Nothing is ever split across a flood of
  messages to fit.
- **A deep backlog is summarized rather than replayed**, so the individual
  messages behind a digest line are in the durable records and not in the
  channel. What is recent, and anything critical, is always said in full.
- **A log line the sink cannot read is read past, not repaired.** The reports,
  proposals, watch, usage-limit, released-claim, and conversation logs are read
  by position, and a line in one of them that will not decode keeps its
  position: it is said once and the records after it are delivered as they
  would have been. Whatever that line recorded is not said, and nothing here
  rewrites the file — `yoyo status` and `yoyo reports` still refuse a log with
  such a line rather than reading it as complete, and name the line.
- **Reporting is not an audit trail.** The durable records under the state root
  are; this is a view of them. `yoyo status`, `yoyo reports`, and `yoyo cost`
  read the same records from the command line.
- **A reply is acted on by the sink that is running.** One killed between reading
  a reply and recording it leaves nothing behind — Slack considers the message
  delivered — so a directive you sent and saw no answer to was not recorded.
  `yoyo directive list` is the check, and the reply can simply be sent again.
- **Who may steer is read when the sink starts.** Granting somebody
  `direct-work` reaches the channel when the sink is next restarted, not while it
  is running. The same holds for who is asked to decide a stopped line.
- **An ask is remembered by the machine that made it.** The decision map is
  beside the thread map and has the same shape of limit: a sink started fresh
  against a state root that has none asks about a state that is still standing
  once more. What that costs is a repeated question rather than a lost one.
- **Old asks are eventually forgotten.** The map keeps the state you are being
  asked about now and the most recent few dozen behind it, so it cannot grow
  until it stops loading. An ask stays answerable well after its state clears — a
  decision made late is still a decision — but a thread from months ago
  eventually stops being read, and a reply into one gets no answer.
- **Nothing is carried out from a direct message.** A decision is recorded as a
  directive and read by the runs that follow; the harness does not lift its own
  holds or start a watch session because somebody answered. `yoyo directive list`
  shows what was recorded.

## When it does not work

| What you see | What it means |
| --- | --- |
| `SLACK_BOT_TOKEN is not set` | The tokens are read from this process's environment only, and the launcher in step 6 is what puts them there. Check that the store in step 5 has this product's pair. |
| `slack refused chat.postMessage: not_in_channel` | The app was never invited to the channel. `/invite @yoyodyne` in it. |
| `slack refused chat.postMessage: channel_not_found` | The channel id or name in `.yoyodyne/config.yaml` is not one this app can see. Check it against the channel's About panel. |
| `slack refused chat.postMessage: missing_scope` | The app was installed before the manifest's scopes were complete. Reinstall it from *OAuth & Permissions*. |
| `a reply could not be marked as <mark>` | The same missing scope, on a reply rather than on a thread's opener: the answer in the thread said what happened and the reaction saying where the directive stands could not go on. Reinstall from *OAuth & Permissions*. A mark that is missed is not set later — what carries the account is the thread. |
| `the reply that asked for this could not be marked as settled` | The outcome was said in the thread and tagged to whoever asked; only the mark on their own message could not be moved. Same remedy, same reason it costs nothing else. |
| `a direct conversation with <member> could not be opened` | Usually `conversations.open: missing_scope` on an app installed before the manifest asked for `im:write`, or a member id that is not in this workspace. The messages this affects are the ones that report the harness itself degraded — a stale session build, the harness having started nothing at all, the provider holding every role, the brake having held intake, a brake hold escalated to you by the development manager or by the harness at the bound on its summons-and-probe loop, a brake hold that has waited on you for two hours, and a claim the harness gave back — and the findings that need the operator's hand; all of them are recorded either way; reinstall from *OAuth & Permissions* and the next one reaches them. |
| `the stopped line could not be put to <member>` | The same refusal on the ask: usually `conversations.open: missing_scope` on an app installed before the manifest asked for `im:write`. Reinstall from *OAuth & Permissions* and the next heartbeat asks. Reporting into the channel is unaffected, and until it is fixed the stopped line is said there and nobody is asked. |
| A decision reply that is never answered | The app can open the direct message but cannot read the reply: `im:history` and the `message.im` event are what carry it back. Reinstall from the current manifest. Nothing was recorded, so answer again once it is. |
| `the watch session's build <sha> is not a revision this product's repository holds` | Said once per build, and not a fault. How old a `yoyo work --watch` session is is measured by counting what has landed in the repository since its binary was built, and that only means anything where the product this sink reports on is Yoyodyne's own source. For any other product the comparison is not this sink's to make, so it says so once and stays quiet. |
| `the status mark on <item> could not be set` | Usually `reactions.add: missing_scope` — an app installed before the manifest asked for `reactions:write`. Reinstall it from *OAuth & Permissions* and the marks appear on the next pass, without the items having to move again. The messages are unaffected either way, and this is said once rather than every pass. |
| `Your manifest has Socket Mode enabled, which requires additional setup` | Slack cannot mint the app-level token until the app exists. Create the app, then generate that token under *Basic Information* and turn Socket Mode on if it is still off. |
| `slack refused apps.connections.open: invalid_auth` | The app-level token is missing, wrong, or lacks `connections:write`. Generate a new one on *Basic Information*. |
| `Slack will keep refusing this until somebody changes something in the workspace` | One of the four above. It is said once and then retried quietly, so fix it and watch for the line that says messages are being accepted again. |
| `another Slack sink is already running for this product` | You started a second one. The first is still reporting; nothing was lost. |
| Slack says it is `not displaying some messages sent by this application` | Slack suppressed messages for volume, and suppressed ones are hidden rather than delayed. The sink paces itself below that threshold, so seeing this means something else is posting as the same app into the same channel — a second sink, or another integration sharing the app. What was suppressed is still in the durable records. |
| `slack reporting is not enabled` | The project has not opted in. Set `slack.enabled` and `slack.channel`. |
| `replies in these threads are acknowledged and not acted on` | Said once when the sink starts: nobody in this project holds `direct-work` with a bound `slack_member_id`, so no reply steers anything. Step 4 is where that is written. |
| `no human in this project holds direct-work with a bound Slack member id, so nobody may talk to the Lead Product Manager from here` | The other half of the same line, said once at startup: where things stand is all this channel will answer until somebody holds that grant. Step 4 again. |
| A reply is answered `the reply is from somebody this project has not granted direct-work` | Your member id is not bound to a human with that grant, or is bound to a different one. Your profile → *Copy member ID*, and check it against `operators` in `.yoyodyne/config.yaml`. |
| A message is answered `I don't know you` | Your member id is bound to nobody in the `operators` mapping. An entry with a `slack_member_id` and no grants is enough to be recognized; `direct-work` is the separate grant that lets you steer. |
| A reply is answered `Heard as a question rather than an instruction` | You ended it with a question mark, so nothing was recorded and it went to the Lead Product Manager; the answer follows in the same thread. If you meant it as an instruction, say it as one. |
| A reply is answered `that reads as either a question or an instruction` | It opened like a question and ended with no mark, or asked something and went on. Nothing was recorded and nothing was asked. End it with a question mark to ask the Lead Product Manager, or say it as an instruction to record it. |
| A reply is answered `that reads as a question, and` … | It was a question and nobody could answer it from here — this sink was started without the Lead Product Manager's conversation, or the Lead Product Manager was mid-turn. Nothing was recorded. `yoyo chat` is where to ask it, or say it again once that turn lands. |
| A reply gets no answer at all | It was not in a thread this sink opened, or it was not a reply — a message at the top of the channel addresses no work item. Reply inside the item's thread. It is also what a second message gets from somebody this project does not know: they are told once per thread, and read after that. In a direct message it is a thread the sink never asked in, or an ask old enough to have been forgotten; `yoyo directive record` at the terminal records the direction either way. |
| A message to the app is answered `Where things stand is what I can tell you` | You are recognized and do not hold `direct-work`, which is what talking to the Lead Product Manager takes. Asking where things stand still works. Step 4 is where the grant is written. |
| A message to the app is answered `The Lead Product Manager is mid-turn with another client` | Another client was taking a turn at that moment — a `yoyo chat` answering at a terminal, or the harness delivering something to the Lead Product Manager. Nothing was said; say it again once that turn lands. A `yoyo chat` waiting at its prompt holds nothing, so closing one changes nothing, and the channel itself only ever holds the conversation for the length of one answer. |
| A message to the app is answered `I waited 10m0s for the Lead Product Manager` | The turn did not finish inside the channel's bound. A turn waiting out a provider usage window releases the conversation during each wait, so other messages can reach the Lead Product Manager; `yoyo status` names the wait and its cause. While a provider is actively answering, the turn holds the conversation. `yoyo chat` queues behind that active turn, says so, and continues from where it lands. `yoyo agent list` says whether the Lead Product Manager is mid-turn without waiting on it. |
| A message to the app is answered `The Lead Product Manager could not answer:` | The provider's own reason follows the colon. An exhausted usage limit says so there, and also reaches this channel as a warning through the ordinary reporting. |
| A message to the app is answered `That decision could not be carried out, and the Lead Product Manager was not asked:` | You approved or declined a proposal from here and the harness could not carry it out whole — the tracker refused the item, or the proposal was already decided or no longer held. The harness's own reason follows the colon, and it says whether any part landed; what did land is posted just above it. No turn was spent. |
| A message to the app is answered `That is a command` | You typed one of the `/` commands at the app. They are your own authority rather than the Lead Product Manager's, and `yoyo chat` or `yoyo` at the terminal is where they are carried out. Nothing was said and no turn was spent. |
| Nothing is posted at all | Nothing has happened since reporting on this product began that it had not already said. Run something; work that finished before that moment is deliberately not replayed, and the first pass prints which moment it is. |

Every row above is something you saw. What a stopped, stale, or misdirected sink
gives you is silence, so those are asked for rather than watched for:

```sh
yoyo doctor
```

| What it says | What it means |
| --- | --- |
| `this project's Slack secrets are not stored` | No pair under this product's names. The remedy is the two `security add-generic-password` lines, filled in for you. A generic pair, or a sibling project's, does not count and deliberately does not pass. |
| `no sink is running for this product, so nothing is being reported` | Nobody holds this product's lease. If a sink recorded itself here before, the line names which build it was and when it started, because a sink that died and a quiet week are otherwise the same silence. |
| `the running sink is an older build than the installed one` | The binary moved and the process did not. It is still posting what its own build knew how to post and dropping everything added since. The remedy stops it by the pid it recorded and starts the right one. |
| `the running sink holds <other>'s secrets, not <this>'s` | It was launched from a shell carrying another project's pair, and is posting this project's work through that project's Slack app. The workspace it actually authenticated into is named beside it. |
| `the running sink was started from a shell rather than from this project's launcher` | It may well be right; nothing recorded whose tokens it holds, so nothing can say. Restart it through the launcher in step 6. |

## Where the tokens go, and what the harness guarantees about where they do not

The sink is a separate process so that the credential boundary is structural,
and the harness holds the other half of that boundary itself: **no agent, no
check, and nothing either of them starts is ever given a Slack token, whatever
the shell that launched the harness happened to carry.** Every process the
harness launches for a run or a conversation — the provider's own binary, and
the project's checks — receives an environment the harness builds from an
allowlist rather than one it inherits: what a program needs to run at all
(`PATH`, `HOME`, `USER`, `TMPDIR`, the locale, the proxy and certificate
settings), the provider's own variables (`CLAUDE_*` and `ANTHROPIC_*` for Claude
Code, `CODEX_*` and `OPENAI_*` for Codex), what the toolchains a check runs read
(`GO*`, `XDG_*`, Git's environment configuration), the harness's own
`YOYODYNE_*`, and the two things the harness sets for itself — the build cache
and the Git maintenance fence. Everything else stays with the harness, and
whatever the allowlist admits, a name that reads as a credential — anything
`yoyo` would redact from a process's output: `*TOKEN*`, `*PASSWORD*`,
`*API_KEY*`, `*_SECRET`, and the rest — is dropped as well. So
`SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` are kept out twice over, and a test
launches a child with both exported and holds it to not seeing them.

The same rule reaches a provider's own key: `ANTHROPIC_API_KEY` or
`OPENAI_API_KEY` exported in the shell does not reach an invocation either. The
provider authenticates from its own login, held in its provider home, which is
what an [account](../configuration.md#provider-accounts) names — a key in the
environment was never the supported way in, and now it is not a way in at all.

What the harness cannot do is keep a token out of *its own* environment: a
harness started from a shell that exported the pair holds the pair, and so does
any other process that shell starts. That is why the tokens still go where steps
5 and 6 put them rather than in `.yoyodyne/config.yaml`, in a work item, in a
prompt, or in a shell profile: a store only the sink's own launch reads, under
names that carry the product. The launcher form matters as much as the store —
the assignments are on the `exec` line, and the environment file is sourced
inside a subshell, so the tokens exist in the sink's environment and never in
the shell you started it from. Your shells stay clean and exactly one process
ever sees the credentials.

The plain `export SLACK_BOT_TOKEN=…` form works and is the wrong thing to leave
running, for a reason the allowlist does not remove: everything started from
that shell inherits the pair, so the second harness on the same machine gets
whichever project's tokens that shell happened to have, and posts one project's
work into another project's channel while looking entirely healthy. Its agents
still see nothing; its sink is the one that is wrong.

Nothing else on your machine ever reads these secrets. `yoyo doctor` asks whether
they are *stored*, in the form that answers without producing the value — the
keychain is queried for the item rather than for its password — because a
diagnostic that helpfully printed a token would put it in a terminal, a
scrollback, and whatever collects them.
