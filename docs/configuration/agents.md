# Configuring operators, personas, and reporting

The humans this project recognizes and what each may do, the provider accounts
agents run under, how agents ask one another and how long their turns may take,
the research sources, pinned files, tools, and scheduled message intake a
conversation is given, the recurring tasks that wake a role on a schedule, the
personas that specialize a role, and what reaches Slack.

[The configuration index](../configuration.md) lists the other guides.

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
cost — so it reaches [the pile you read](../reporting.md#what-agents-report-and-where-it-reaches-you)
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
the turn is answered, the way [`/refresh`](../conversation.md#how-fresh-the-conversations-picture-is-and-how-to-refresh-it)
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
[failover](recovery.md#serving-a-turn-from-a-permitted-alternate-model) is. Stating it empty
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
and it is served by the agent's [permitted alternate](recovery.md#serving-a-turn-from-a-permitted-alternate-model)
and [pinned version](recovery.md#pinning-an-agent-to-a-model-version) exactly as a main turn
is.

## How long an agent's turns and calls may take

Each agent's turns, messages, and provider calls are bounded in time. The
figures below are the harness's, and an agent may state its own in its block,
under `time_limits`:

```yaml
agents:
  technical-health-pm:
    role: program-manager
    time_limits:
      turn: 45m              # one provider turn in its conversation
      message: 1h30m         # one message, however many turns it takes
  reviewer:
    role: reviewer
    time_limits:
      review: 30m            # one review
  developer:
    role: developer
    time_limits:
      provider_call: 6h      # one developer provider call
```

| Key | Who it is for | What it bounds | Default | Range |
| --- | --- | --- | --- | --- |
| `turn` | an agent that holds a conversation | One provider turn in the agent's conversation; it is the provider call's own total time. A recurring pass that wakes the agent starts counting it once the pass holds the conversation, so time spent waiting for the conversation to be free does not count against it. | 15 minutes | 1 minute to 4 hours |
| `message` | an agent that holds a conversation | The elapsed time of one message, however many turns answering it takes, not counting time spent waiting out a provider that refused. It also bounds how long a recurring pass's turn may wait for the agent's conversation to be free; a pass whose wait reaches it is recorded as missed. | 45 minutes | 2 minutes to 8 hours |
| `message_turns` | an agent that holds a conversation | How many provider turns one message may take. | 16 | 1 to 100 |
| `message_spend_usd` | an agent that holds a conversation | What one message's turns may cost, where the provider reports a cost. | none | above 0 to 1000 |
| `slack_turn` | an agent that holds a conversation | One turn taken from the Slack channel, from the message arriving to the answer being posted. The channel talks only to the Lead Product Manager, so hers is the one that applies. | 10 minutes | 1 minute to 4 hours |
| `exchange_answer` | an agent that holds a conversation | The agent's answer to a question [another role asked it](#how-long-one-role-may-ask-another). | 5 minutes | 1 minute to 1 hour |
| `side_thread_turn` | an agent that holds a conversation | One turn on a [side thread](#queueing-a-question-or-holding-it-on-a-side-thread). | 5 minutes | 1 minute to 1 hour |
| `review` | the reviewer | One review. | 15 minutes | 1 minute to 4 hours |
| `provider_call` | the developer | One developer provider call, its tool work included; another call starts another clock. | 4 hours | 10 minutes to 24 hours |

The agents that hold a conversation are the Lead Product Manager, the
architect, the development manager, and every program manager instance; each
instance has its own block. A limit an agent does not state keeps the figure
above, which is what it was before these keys existed. A block in a later layer
replaces an inherited one whole, as the failover block does.

Each value is checked when the configuration is read, and the file is refused,
naming the agent, the key, and the value, when a value is outside its range or
is set on an agent whose role nothing applies it to: a `review` on the
architect, say, or a `turn` on the developer. A `turn` that is not shorter than
the same agent's `message` is refused too, defaults included, because a turn as
long as its message would be stopped by both limits at once. So raising `turn`
to 45 minutes needs `message` raised past 45 minutes as well.

Two limits stay the harness's whatever is set here. A provider session that
produces no output for five minutes is stopped, however long its turn or call
may take, and the harness carries on a run whose session went silent only
once. A longer limit lets a session that is working run longer; it never lets
a silent one sit. [What stops a run](../run-stops.md) lists both.

A role may apply `agents.<name>.time_limits` to the configuration, as it may
the check time limits: it changes how long work may take and never what an
agent may do. `time_limits` is a key a build from before it cannot read, so a
file carrying it is refused by any part of the product still running an older
build until that part restarts on one that can; `yoyo config validate` names
which.

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
in [the conversation guide](../conversation.md#bringing-it-an-idea-rather-than-a-work-item).

## Pinned outside files

Some work needs a file nobody in the project wrote, such as a chart library the
dashboard serves. Developer runs cannot reach the network, so the harness brings
such a file in itself, in its own process, from a record you keep beside it
([the design](../designs/pinned-outside-files.md)). **Nothing is fetched until you
list a host.**

```yaml
outside_files:
  hosts: [cdn.jsdelivr.net]   # host names, or host:port where the address has one
  max_bytes: 5242880          # the largest file fetched; 5 MiB when left out
```

**The record.** Beside the pinned file, under its name with
`.yoyodyne-source.yaml` added, write `file`, `url`, `version` (which must appear
in the address's path), `license` (an SPDX identifier), `license_file`, `for`
(the work item that asked for the file), and, where the source publishes one,
`sha256`. The harness writes `sha256` where you gave none, `size`,
`fetched_from` and `fetched_at` when it fetches; nothing else may write those
four.

**What is fetched, and how.** At each pull the scheduler reads the records on
the target branch, and only those: a record still on a run's branch is never
fetched. A record is due when its file is missing or it carries no checksum.
The address must be https, on a listed host, with no user name, password, query
string or fragment; at most five redirects are followed, each to https and a
listed host; the file is held to `max_bytes`, and to 50 MiB whatever that says;
no credentials are sent; and the file is never unpacked or run. What the fetch
brought is landed by a run of the harness's own: one at a time, holding no
developer slot, changing only the file and the record's four fields, with the
full checks and an independent review shown the record, the final address, the
size, the checksum, and the license file. The checks are told the whole module
rather than only the packages the file sits in, so what passes is what the
merge step needs. Where the record's `license_file` has a record of its own and
the target branch does not hold it yet, the same run fetches and lands the
license too, so the file's checks see the license beside it, and the license's
record does not start a run of its own meanwhile. While the license's record is
stopped or waiting, the file waits with it, and the other way round. It merges
through its pull request, and through the merge queue when
`execution.merge_queue` is on, like any approved change. Where the queue's combined change fails its checks or its
review, or will not apply to the target as it then stands, the run ends with the
reason written on the work item the record names, nothing is handed to a
developer, and a later pull starts a fresh run from the record as the target
branch then holds it. Three such refusals in a row for one record stop it and
file one warning report for the development manager naming the record and the
three reasons.

**Every run compares.** Before its checks, every run compares each pinned file
with its record. A file whose checksum does not match, or a record whose four
harness fields a change wrote, fails the checks naming it. A missing file is
fine: the harness fetches it. To change a version, edit `url` and `version`,
clear the four fields, and remove the old file.

**When a fetch fails.** A network failure is tried again at least ten minutes
later and counts for nothing. A failure about the record — an address the rules
refuse, a missing file, a file over the limit, a checksum that does not match,
a `for` naming no work item, a check or a reviewer refusing the file — stops the
record until it changes on the target branch, and files one warning report for
the development manager. A failure about either the file's record or its
license's record, where one run brings both in, stops both and brings in
neither. A run the merge step refuses because its record does not show the
checks and review it needs in a way another run would repeat, such as checks
that ran over only part of the module, stops the record too, and the report
says which check was missing; since the record is not at fault, it is tried
again once a new build of the harness is running, as well as when the record
changes. A merge step that could not read the change, or found
it moved since its checks, is tried again after a wait, and three such runs
in a row stop the record.

## MCP servers, and the tools each agent may ask for

A project can declare MCP (Model Context Protocol) servers, programs that offer
named tools, and list, for each agent, the exact tools that agent may ask the
harness to call on its behalf
([the design](../designs/mcp-tools-through-the-harness.md)). No agent ever holds a
server, a connection to one, or its credentials: the harness makes every call
itself. **In this build a conversation — the Lead Product Manager, the
architect, the development manager, or a program manager — can ask for the
tools on its own agent's list, and a developer run can ask for the read tools
on its own agent's list.** A project that
says nothing has no servers and no lists, and runs exactly as it did before the
keys existed. [Using MCP servers](../mcp.md) is the setup guide, in order, with a
worked example to copy, and says what a result sent to an AI provider means for
private data.

```yaml
mcp:
  servers:
    mail:
      command: [mail-mcp, --stdio]   # a program the harness starts on this machine
      login: [mail-mcp, login]       # optional; `yoyo mcp login mail` runs it
      tools:
        search: read                 # changes nothing outside the harness
        send: act                    # may change something
    tickets:
      url: https://mcp.example.com/  # a remote server, https only
      credential: file               # or keychain; never a path
      tools:
        list_open: read

agents:
  product-manager:
    mcp_tools: [mail.search, mail.send, tickets.list_open]
```

- **A server** has exactly one of `command` or `url`. A `url` is https, with no
  user name or password in it. Its redirects are followed only on its own host,
  at most five, so a credential is never sent anywhere else.
- **`tools`** names every tool of the server the project will use, each marked
  `read` or `act` by you, not by the server. A tool it does not name cannot be
  called by anyone.
- **`mcp_tools`** lists exact names, written as server and tool, with no
  wildcards. Loading refuses a list that names a server or tool the `mcp`
  section does not declare, and one naming a tool whose class the agent's role
  may not hold, naming the agent, the tool and why. The Lead Product Manager,
  the architect and the development manager may hold both classes; a program
  manager and a developer hold `read` tools only; a reviewer holds none. That
  table is in the harness's code, so no configuration widens it.
- **A credential is named by its store, never by a path.** `keychain` is the
  item `yoyo-mcp.<product id>.<server>` under the account `yoyo`; `file` is
  `<server>.token` in the `mcp` folder of the product's state directory,
  readable by your user alone. Nothing else is read: a link at that path, or a
  file other users can read, is refused. A server the harness starts is handed
  its credential in the environment variable its `credential_env` names, and
  such a server that names a credential has to name that variable too.
- **A local server's login** is kept in its own folder, `mcp/<server>/` in the
  product's state directory. `yoyo mcp login <server>` runs the declared login
  at your terminal with that folder as its working directory and its home. It
  is refused to a process the harness started for a role. A server with no
  login prints the command that stores its credential instead.
- **The repository a server reads** is handed to it by the harness, in the
  environment variable `YOYODYNE_REPOSITORY`, and never chosen by a role: for
  a developer run it is that run's own worktree, and for a conversation or a
  recurring task it is the main checkout. Every call starts its own server, so
  no server is shared between runs. A server confined to one repository names
  it from that variable, such as
  `command: [sh, -c, 'exec mcp-server-git --repository "$YOYODYNE_REPOSITORY"']`,
  so a developer run reads its own branch and is refused the main checkout and
  every other run's worktree. A command that names a fixed path instead reads
  that path for every caller, and a developer run's own worktree is refused. A
  linked worktree keeps its Git data under the main checkout's `.git`, outside
  the worktree; the official git server accepts that as the worktree's own.
- **A server confined to this repository** sees the repository's whole `.git`
  directory. The official git server, started with `--repository`, refuses
  every call while any symbolic link in there leads outside the repository. A
  developer run's scratch folder is therefore not there: it is
  `scratch/<run id>/` in the harness's state directory, so links a run leaves
  in it, such as the links to its own program the Codex CLI leaves under a
  provider home made there, are nowhere the server checks, and the folder is
  removed with the run's worktree
  ([the ruling](../decisions/developer-run-scratch-outside-git.md)). Scratch
  folders that runs from before this were given under `.git` are removed by
  the supervisor's maintenance pass, in its `scratch` step, once each
  folder's run has ended
  ([the maintenance pass](../operations.md#the-supervisors-maintenance-pass)); a folder whose run is still going
  is left until it ends.
- `yoyo config show` reports each agent's MCP tools beside its other tools, and
  `yoyo doctor` reports each server whose credential is missing, naming the
  server and the command to run. A missing credential stops nothing else.
- **The record of calls** is kept under the product's state directory, in
  `mcp-calls/`, one entry per call, and stays on this machine. `yoyo mcp calls`
  lists it by who asked, the tool, and how each call ended, and prints a call's
  arguments and result only for `--entry` or `--json`, which a process the
  harness started for a role is refused. `yoyo status` and the dashboard show
  the last 24 hours of calls by tool and outcome and never what a call sent or
  returned; neither does anything the harness writes to the tracker or Slack.
- **A server whose tool listing changes** from the one it gave at its last call
  gets one note filed for the development manager, naming the server and the
  two listings' digests.
- Both keys are yours alone: a role's write to the configuration is refused for
  `mcp` and for any agent's `mcp_tools`.
- **How a conversation asks.** An agent with tools on its list is told, in its
  instructions, each tool's name, its class, the parameters the server lists
  for it, and the server's own description of it, cut at 500 bytes and marked
  as untrusted text from the server. It asks by ending a reply with a
  `yoyodyne-mcp` block, and the harness makes the calls and hands back what
  came of each in the same message: the result marked as untrusted data, or
  why the call was refused or failed. At most 5 calls a reply, 4 replies with
  calls a message, 32 KiB returned from one call and 64 KiB from one reply. An
  agent whose list is empty is told nothing about MCP, and its instructions are
  unchanged. Each call is kept in the call record under the product's state
  directory, and recorded on the conversation as `tool.requested`, then
  `tool.performed` or `tool.refused`, with the tool's name, outcome and size
  and never its arguments or result. The servers are asked for their listings
  once each time a conversation is opened.

## Scheduled message intake

A project can declare queries the harness will run on a schedule, reading new
messages from a source such as a Slack channel so that a matching message can
start work under a written rule
([the design](../designs/scheduled-message-intake.md)). **In this build a
watching session runs each query on its schedule and writes down every message
it returns; nothing reads a message with a model, starts work from one, or asks
anybody about one yet.** A project that says nothing has no queries, and its
configuration and its revision are exactly what they were before the key
existed.

```yaml
intake:
  queries:
    slack-bugs:
      tool: slack.search             # a read tool declared under mcp
      arguments: {query: in-bugs}    # literals, sent on every call
      since_argument: after          # filled with the start of the time window
      until_argument: before         # filled with its end
      more_field: has_more           # optional: says the source held some back
      every: 1h                      # the default; never less than 5m
      items: messages                # where the messages are in the answer
      fields: {id: ts, sender: user, text: text, sent_at: ts, link: permalink}
      senders: [U123, U456]          # or anyone: true, never both
      reader: message-reader         # the default, and the only reader there is
      rules: docs/intake/rules.md    # the path of the rules document
      questions: lead-product-manager  # the default, or person
      limits: {messages_per_run: 20, messages_per_day: 100, starts_per_day: 5, questions_per_day: 5, text_bytes: 4096}
```

Loading refuses, saying what is wrong in plain words:

- a `tool` that is not declared under `mcp` as `read`, because a query runs
  with nobody watching and must change nothing outside the harness;
- a query without both `since_argument` and `until_argument`, or naming one
  argument for both, or setting either among its literal `arguments`;
- a query with neither a `senders` list nor `anyone: true`, or with both;
- an `every` shorter than five minutes;
- any `intake` section while `team.enabled` is on, because each machine keeps
  its own record of the messages it has read, so two machines could both start
  work from one message;
- a missing `items`, a missing `id`, `sender`, `text` or `sent_at` field, a
  `reader` other than `message-reader`, a missing `rules` path or one that is
  absolute, climbs out of the repository, or does not end in `.md`, a `questions`
  value other than the two above, and a limit below 1. A limit left out takes
  the value shown above.

`yoyo config show` prints one line for each query, saying what it calls, how
often, and whose messages it takes, with `anyone: true` named in that line and
in the effective configuration below it wherever it is set. The whole section
is yours alone: a role's write to the configuration is refused for `intake` and
everything under it.

### How a query runs

A `yoyo work` session runs each query once for every `every` of its schedule,
beside its pulls and in a goroutine of its own: a query run holds no developer
slot, and a run that falls due while the query's previous run is still working
is skipped. The intake hold does not stop a query; your `yoyo pause` does, and a
paused run spends nothing.

- **The first run reads nothing.** It records the moment the query was
  switched on, and every later run reads only messages sent after it, so
  switching a query on never reads old messages.
- **A run reads one time window to a call**, from the time the query has read
  up to and never closer than ten minutes to now. The harness fills
  `since_argument` and `until_argument` with the window's start and end, as
  times in RFC 3339, in UTC, to the second (`2026-11-19T09:00:00Z`).
- **An answer that is cut** — more than the 32 KiB one call may return, or
  `more_field` true — writes nothing down and halves the window. **An answer
  that fits** has every message in it written down, then moves the time read up
  to on, and the next window is twice as long. A run makes at most five calls,
  and the next run carries on from where it stopped.
- **A minute still too large to read** is recorded as skipped, the query moves
  past it, and one warning goes to the development manager. The messages in
  that minute are not read; `yoyo status` counts the minutes skipped.
- **A message from a sender not on `senders`** is recorded as ignored, with its
  identifier and sender and none of its text, link or digest.
- **Each message is recorded once**, under the server's name and the source's
  own identifier for it, with a short number made from that key; a message two
  queries return, or one returned again after a restart, is still one record.
  A message's text is kept up to `text_bytes`.
- **A call that fails** is tried once more by the MCP call path; if it still
  fails, or the answer does not hold `items` and `fields` as declared, the run
  fails and the time read up to does not move, so the next run asks again and
  nothing is lost. After three failed runs in a row one warning is filed, and
  no other until a run succeeds: to the development manager where calls fail,
  and opening with `protected-file` where the answer has the wrong shape,
  because only a change to the query's `items` or `fields` in this section can
  mend that.

The record of messages is kept on this machine under the product's state
directory, in `intake/`, and is only ever added to. The MCP call record keeps a
query's calls with the size and digest of each answer and whether it was cut,
and never the answer itself, so `yoyo mcp calls` shows a query's calls with no
content and the intake record is the only copy of any message. `yoyo status`
prints one line for each query: how far it has read, how many messages it has
recorded and ignored, the minutes it skipped, and how many of its last runs
failed.

### The rules document

`rules` names the Markdown document a query's messages are judged by, by its
path in the repository. Where you keep it is yours to choose; the harness fixes
no path. Its rules are listed under a `## Rules` heading, each opening with an
identifier in square brackets and saying which messages it covers, with its
parts indented under it:

```markdown
## Rules

- [slack-bug-report] A person reports something in the product that is broken.
  Not a feature request, and not a question about how to use it.
  - workflow: delivery
  - goal: nearly-autonomous
  - priority: 1
  - type: bug
  - label: bug
  - title: Fix the problem reported in a message
  - done: The reported behaviour no longer happens, and a test shows it.
  - from: the direction this rule came from
```

`workflow`, `goal`, `priority`, `type`, `title` and `done` are required, and
`label` and `from` are optional. Everything about the work a message starts
comes from the rule and nothing from the message. The document may carry
frontmatter, and where it does it has to say `kind: rules` and `status:
active`.

The document is read as the target branch holds it when each query run
starts, never from a working tree, and that commit is what the decisions on the
run's messages are recorded against. A rule is refused, and the others still
apply, when it is written so that it cannot be read, names a goal that is not
an active goal by its identifier, sets priority 0 (a message from outside does
not go ahead of reliability work without somebody deciding that), names a
workflow other than `delivery`, or shares its identifier with another rule. A
document that cannot be read as rules at all — missing on the target branch, no
`## Rules` heading, or no rule that can be used — stops that query's readers
and is reported once to the Lead Product Manager; its messages wait, and none
is lost. A rules document a query names is read by the message reader and is
not given to every role as product intent, even when it is kept under the
product's specification home. **In this build queries run and record the
messages they return, but nothing judges a message against the rules document
yet; that waits for the message reader.**

## Slack review threads

A project whose pull requests a person merges can ask the harness to keep a
Slack thread for each, using a server and two tools the `mcp` section above
already declares. This section only says what loads; the design is
[Slack review threads](../designs/slack-review-threads.md), and nothing yet posts
or reads a thread. With no `review_threads` section nothing changes.

```yaml
review_threads:
  server: slack                 # a server declared under mcp
  post_tool: post_message       # declared there as act
  read_tool: read_thread        # declared there as read
  channel: C0123456789          # Slack's id for the channel, never its name
  reviewers: [U0AAAA1111]       # Slack member ids whose replies are told
  check_every: 15m              # optional; at least 5m, at most 24h
```

- The configuration is refused when it loads, naming the setting, if `server`
  is not declared under `mcp`, `post_tool` is not declared there as `act`,
  `read_tool` is not declared there as `read`, `reviewers` is empty, or
  `check_every` is under 5 minutes or over 24 hours. `channel` is also required.
- `check_every` is 15 minutes when left out.
- `yoyo config show` prints the section, and `yoyo doctor` says a section that
  loads is set, or names the refused setting in plain words.
- The key is yours alone: a role's write to the configuration is refused for
  `review_threads`.

## Reading the repository from a conversation

The three management roles — Lead Product Manager, architect, development manager —
and the program manager can have the harness read one repository path for them, or list the names one
directory holds, at a recorded commit. It is here beside research because it is
the same shape and the opposite arrangement: research is evidence from outside
the repository, run by a command you wrote and off until you name one; this is
evidence from inside it, run by the harness's own Git, and **there is nothing to
configure**. No key switches it on, none switches it off, and none moves a
bound. [The conversation guide](../conversation.md#reading-the-repository-at-a-recorded-commit)
says how a role uses it; what belongs here is why the file you are reading has
no say in it.

**Which roles hold it is the role-capability registry's, in Go.** The three
management bundles and the program manager's hold `repository.read` and
`repository.list`, and the program manager's alone holds `repository.search`
beside them; the developer's
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
being cut with the cut declared, its whole size named, and the bytes that remain
counted, and read on in parts by naming `"offset"` in the next request; a listing returns at most 400 names; one message reads at most
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
— the same label its [shipped documentation](artifacts.md#what-the-lead-product-manager-sees-besides-them-and-what-it-does-not)
carries, applied on every delivery, with the same rule: where a file contradicts
a specification, the conflict is reported rather than resolved. The
specifications remain the only statement of what the product is for.

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
provider](../provider-plugins.md) whose `adapter` is `claude-code` — authenticate in
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
- **`billing`** is how the account is paid for: `api_key` (per token at the
  provider's API prices), `subscription`, or `credits` (purchased credits), and
  `not_recorded` where it is left out. It is the one place the harness learns
  this, and it is never inferred from a model or a provider. It decides what a
  cost the harness works out for a call the provider did not price is: on an
  `api_key` account it is an estimate of money spent; on a `subscription` or
  `credits` account it is what the same use would cost at API prices, shown as
  a comparison and never as money spent, with credits shown apart where the rate
  data prices them; and on an account that does not say, the cost is shown as
  unknown. A provider's own reported cost is shown as reported whatever this
  says. Any account may carry it, including a single `default` one. See
  [the provider usage and cost design](../designs/provider-usage-and-cost.md).

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
start](../multi-account-quickstart.md), and `bin/yoyo-account`
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
- `dashboard_token` — where their own dashboard token is kept, `keychain` (the
  item `yoyo-dashboard.<product id>.<name>` under the account `yoyo`) or `file`
  (`<state root>/projects/<product id>/state/dashboard.<name>.token`, which the
  dashboard creates with a fresh token, readable by the user the harness runs
  as alone, where it is not there, and never replaces where it is). It names
  a store and never holds the token. A dashboard tab signed in with that token
  is this person, which is what lets [the page take their own approve and
  decline](../operations.md#deciding-from-the-page); the dashboard's shared token resolves to nobody. A running
  dashboard reads the operators and their tokens again every ten seconds, so a
  person bound, or a token stored, changed, or removed, is applied without
  restarting it. For now only the
  operator — the one human holding `own-intent` — may bind one; a
  `dashboard_token` on anybody else's entry is refused when the configuration
  loads.

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
| `decide-amendments` | deciding a change proposed to a document from the dashboard. A change to a document the Lead Product Manager owns needs `own-intent` beside it. `yoyo amendment` at a terminal carries no identifier to check it against. |

**Grants are checked in two places, and it is worth being exact about which.**
The Slack sink's allow-list is derived from the `direct-work` holders who bound a
member id, so a thread reply is acted on or refused by asking this mapping who
sent it. And the dashboard resolves a tab to the person whose own
`dashboard_token` it presents, and approves or declines from the page only for
a person holding `own-intent` — for a document or proposed work — or
`decide-amendments`, with `own-intent` beside it for a change to a document the
Lead Product Manager owns. Those are the two acts that carry something the
harness can resolve to a person: a member id the workspace put on the message,
and a token the person stored.

**At a terminal `own-intent` is checked by nothing, and `by: operator` proves
only that a command was run.** A terminal carries no identifier at all, so `yoyo artifact
approve` records `by: operator` on the strength of whoever ran it, and so do
`yoyo amendment approve`, `yoyo directive record`, and the `--by` on `yoyo triage
override`, which is a string somebody types. Nothing in any of those records
distinguishes the operator from anything else with a shell. Read an `own-intent`
grant as this project's record of who owns intent — which is what refuses a
second holder when the configuration loads, and what a person auditing the
mapping reads — rather than as a gate an act passes through.

**What keeps an agent out of the goals is two enforcements that do not depend on
the signature.** (The one item the harness admits on its own account — the bug
a [red landing](runs.md#where-the-whole-suite-runs) files — is the harness's act and
not an agent's, and it reaches the queue and never the goals.) A conversation
cannot write: on Claude Code it runs with no tools at all, and on Codex it can
only read, so the roles that could argue for a goal cannot change a file; and a
run's change is compared
against the [protected paths](artifacts.md#protected-paths-in-a-developers-change) before any
check runs and before any reviewer sees it, so an approval a developer wrote is
refused with the rest of the diff and never reaches the repository the goals are
read from. [What reaches the queue](goals.md#what-reaches-the-queue) rests on those two
rather than on who an approval says gave it, which is what
`internal/chat/admission.go` says in its own words. If either is ever loosened,
this is what was resting on them.

At a terminal, making an approval name the resolved human, and refusing one
from anybody who does not hold `own-intent`, was proposed and is not built. The
dashboard refuses a person without the grant, and records what it decides as
the operator's, exactly as the terminal's verb does. The proposal was in an
early team-mode draft that was never adopted;
[the design that replaced it](../designs/working-alongside-other-contributors.md#the-five-gaps-team-mode-named)
takes the forge account as a person's identity and establishes no proof of who
an operator is beyond it.
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
[`docs/slack/setup.md`](../slack/setup.md). Deleting the leading `# ` from each
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
`chat:write.customize` scope the [app manifest](../slack/manifest.yaml) already
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
[`product.id`](setup.md#layout): `Development Manager (yoyodyne)`,
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
[deciding a stopped line from a direct message](../slack/setup.md#deciding-a-stopped-line-from-a-direct-message). A reply from a human this mapping names who is not on it is
answered in the thread saying it was not acted on, naming the grant they are
missing — visibly, because a channel that silently ignores some people looks
broken rather than closed. What a reply may say is in
[`docs/slack/setup.md`](../slack/setup.md#steering-the-work-from-a-thread).

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
in](runs.md#the-environment-the-harnesss-own-git-and-forge-commands-run-in). What such an export does
still cost is the harness's own process and the sink: they are read from a
store only the sink's own launch looks at, under names that carry the product —
`yoyo-slack-bot.<product id>` and `yoyo-slack-app.<product id>`. The product is
in the name because a machine running more than one harness has more than one
pair, and a sink launched from a shell holding the wrong one connects,
authenticates, and posts this project's work into another project's channel.
`yoyo doctor` asks whether this project's pair is stored, and whether the sink
that is running was launched with it; [`docs/slack/setup.md`](../slack/setup.md#5-store-the-two-tokens-under-this-projects-names)
has the launcher.

Reporting is an observation and never a gate: a workspace that is down, slow, or
misconfigured changes nothing about any run. [`docs/slack/setup.md`](../slack/setup.md)
takes a workspace from nothing to live reporting, and the app manifest it asks
for is checked in beside it.
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
[triage docket](../conversation.md#roles-asking-each-other-things) as it stands,
built for each firing and put in the message that wakes her, ahead of the
prompt. A pass resumes her conversation rather than opening it, so the docket
that conversation opened with is not what is waiting on her now.

A Lead Product Manager's task is handed the work closed since that task's last
completed pass, with what landed for each and the audits program manager
instances made of their own lanes, on its first turn, for the audit of closed
work against the standing goals that `yoyo init`'s commented
`product-manager-sweep` example asks for as its third job.
[The operations guide](../operations.md#reading-what-the-recurring-tasks-found)
says what the pass is handed and how a violation is corrected.

Every recurring task turn also receives the live work assigned to that role's
conversation, with each item's priority shown. The harness lists it in backlog
order: highest priority first (P0 before P1), then within each priority the
items the Lead Product Manager placed first, then oldest admitted, using the
same order as a developer pull. Parked and finished items
are excluded. This list is read again for each turn, so an architect's pass
receives a newer P0 design before an older P2 without relying on the prompt to
reorder an old briefing. The role reads each item in full before deciding.
Reports and proposed amendments keep their own ordering, described below.

`yoyo init` includes a commented `architect-pass` example at `every: 1h`, whose
prompt says to take work by priority first, then age within each priority. A
project chooses its own cadence; this repository uses `45m` to keep passes
closer together after the operator observed more cached opening input below
an hour. That observation is a reason for the setting, not a provider guarantee.

The architect is also woken without any recurring task, by the `needs-design`
label. In a project with an agent in the architect's role, a work item
carrying the label that she has not yet been woken for wakes her conversation
at the next pull, with the item named; an item labelled twice wakes her once,
and one that loses the label and gets it again later wakes her again. The
label is read from the tracker as it stands, so it wakes her whichever path
added it: a conversation's label action, an item created with it, or a
developer's raise. If any item still carries the label a day after her last
such pass, she is woken once more with all of them listed, which catches a wake
that was lost; while no item carries it, nothing is taken. While a turn is
going in her conversation, the wake waits for it rather than being refused.
These passes are recorded under `architect-needs-design`, which `yoyo sweeps
--task architect-needs-design` reads, and a recurring task may not take that
name. The wake has no key of its own, so nothing here is needed to turn it on,
and an `architect-pass` is not needed to catch labelled work.

| Key | What it says |
| --- | --- |
| `role` | which role is woken. It must be a role this project configures an agent for; a task naming a role nobody fills is refused rather than discovered as silence. |
| `every` | the cadence, measured from the last firing rather than against a wall-clock grid. The shortest accepted is `5m`, which is what keeps `1m` written where `1h` was meant from becoming sixty times the spend. |
| `enabled` | the switch. It is explicit so a task can be turned off for a week without deleting its prompt and cadence. |
| `prompt` | what the role is told. It is the task, not a personality. |
| `max_turns` | how many work turns one firing may take, defaulting to 3 and capped at 10. Three turns are outside this bound: one request for a missing closing report, one request to correct a closing report the harness refused, and one turn sending the pass back to decide or ask about an item its passes keep leaving ([operations](../operations.md#where-the-harness-stands-the-four-lines)). |
| `model` | the model this task's turns ask for. Optional: leave it out and the turns ask for the role's own configured `model`, which is what every task did before the key existed. See [a task's own model](#a-tasks-own-model). |
| `skip_when_unchanged` | whether a scheduled firing is skipped when nothing its role would be handed has changed since the task's last completed pass. Optional, and on when left out. See [skipping a pass with nothing new](#skipping-a-pass-with-nothing-new). |
| `full_pass_every` | the longest the role goes without a full pass while passes with nothing new are skipped. Optional, defaulting to `4h`; anything under `1h` or over `12h` is refused when the configuration loads. |

### Skipping a pass with nothing new

Before the harness starts a scheduled firing, it checks, without asking any
AI provider, whether anything the pass would be handed has changed since the
task's last completed pass. It looks at:

- the target branch, for a change landed on it;
- the work items the role's conversation carries, is assigned, or, for a
  program manager, that carry its lane's label;
- the unhandled reports addressed to the role (for the Lead Product Manager,
  every unhandled report) and the undecided changes proposed to its documents;
- what is waiting in the role's conversation to be told, which is where a
  question from another role, a message from you, or a document handed back
  arrives;
- the role's documents sent to land, and how each run carrying one stands;
- the follow-ups the role owns whose deadline has passed;
- for the architect and the Lead Product Manager, the unpublished documents
  that overlap an open work item and are not yet answered;
- for the development manager, her list of stopped runs; and for her and the
  program manager whose lane is `factory-flow`, the factory's health problems;
- for a program manager, the events in its lane since its last pass.

Where none of these has changed, the pass is not started. `yoyo sweeps` shows
it as `SKIPPED`, with the completed pass nothing changed since and when the next
full pass is owed; it costs nothing and counts as that window's firing, so the
schedule runs on from it. Anything the check cannot read counts as a change, and
so does a task with no completed pass yet. Every role still has a full pass at
least every `full_pass_every`, whatever the check finds, so a change it does not
look at waits no longer than that. For the development manager and the
`factory-flow` program manager, a health problem still present an hour or more
after the last completed pass counts as a change too, so an idle slot with ready
work or a run left waiting is looked at within the hour even though the problem
has not changed.

What the check reads is fixed in the harness for each role and is not
configurable: a list a project could edit could hide a change from the role that
has to act on it. Passes started by anything other than the schedule (a program
manager's events, the intake brake's summons, a request for a pass, a critical
report) are never skipped. A program manager instance takes the same two keys
under its `triggers` block, applied to its scheduled pass:

```yaml
agents:
  flow-pm:
    role: program-manager
    lane: factory-flow
    triggers:
      every: 1h
      on: [stoppages]
      full_pass_every: 2h
```

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
[the developer mapping](runs.md#a-developer-model-chosen-by-the-items-label): the
management roles on their own model were $317 of $1,431, and most of it was
routine sweep passes. The lines are the operator's to paste into the project's
own configuration by hand, because `.yoyodyne/` is a
[protected path](artifacts.md#protected-paths-in-a-developers-change) no run may write; a
task whose block does not carry the key runs on the role's model.

**It selects a model for the task's turns and nothing more.** The turn is taken
in the role's own conversation, under the account that role's agent names,
holding the authority the role holds and reading the persona it reads — so
configuration still selects and never widens what a role may do. The
[account pool](#pooling-work-across-several-accounts) and the
[failover rules](recovery.md#serving-a-turn-from-a-permitted-alternate-model) apply
unchanged: a pass whose model has no capacity is served by the agent's
alternate where it has enabled one. Two things follow from the model being
another one. A [pinned version](recovery.md#pinning-an-agent-to-a-model-version) is a
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
failover answered — and [`yoyo sweeps`](../operations.md#reading-what-the-recurring-tasks-found)
prints it on each pass's header. [`yoyo status --spend`](../operations.md#following-a-run-a-conversation-or-a-branch-review)
reads the same records and adds a table under its totals: each task's passes,
turns, and cost, by the model they ran on. It is a split of the conversations
figure above it rather than an addition to it, since a pass is conversation
turns. A pass recorded before passes named their model is shown as
`(not recorded)`.

**A firing costs what conversation turns cost.** The cadence is therefore the
spending decision: `every: 1h` is a turn an hour for as long as a `yoyo work
--watch` session is running.

**Each turn of a pass starts a provider session of its own.** It does not resume
the conversation's session. It starts from the role's instructions, its
memories, the product briefing with the product intent whole and the other
documents left out to be read on demand, what the previous recurring turn said,
and the pass's message, bounded to a third of the provider's context window. The
conversation's own session is put back for the turns the operator opens. See
[what a recurring turn starts from](../conversation.md#how-fresh-the-conversations-picture-is-and-how-to-refresh-it).

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
role that fell due before it. [`yoyo sweeps`](../operations.md#reading-what-the-recurring-tasks-found)
says how long each pass waited after it fell due. Before 2026-09-30 a pull made
one firing, tasks first, and took its turns inside the pull: on 2026-09-29 the
Lead Product Manager's several-turn sweep kept the development manager's sweep
and a program manager's pass unfired for more than an hour, four times, and the
critical reports of those misses woke her into the one firing they were waiting
for; on 2026-09-30 one pass that spanned a machine sleep held every pull for
three and a half hours.

**What bounds that spend is the session's own
[`--budget`](runs.md#watching-instead-of-draining)**, which counts a firing's turns
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
to `max_turns`. The one work turn past that bound is the turn that sends a pass
back when it ends having left an item it was told to decide or ask about,
because its passes have left that item twice in a row with nothing outside the
role holding it. A pass that still had more to do when the bound ran out is
recorded as partial, so a truncated pass and a finished one are never the same
short report.

**A missing closing report is requested once on the same pass.** The request
quotes the report's shape and asks for the block alone, without repeating any
action. Every turn after a pass's first is told again what its report must
hold. If a later turn still gives none and an earlier turn of the same pass gave
a valid one, the pass keeps the earlier turns' account, marked as having more to
do and recorded as partial rather than failed; only that turn's findings are
left unrecorded. A later turn, or its request for the report, that fails at the
provider or is cut off keeps the earlier account the same way. If no turn gave a valid report, the pass fails with its
findings unrecorded. Either way earlier writes and every reply's cost stand.
This extra request spends no work-item budget and does not consume a work turn
under `max_turns`. A report the harness refuses for its shape, such as a
decision under `settled` with no assumptions, is handed back once a pass with
the reason it was refused, and the corrected report is recorded; one still
refused fails the pass with both reasons on its record. That request is outside
`max_turns` too. After consecutive passes still omit the report on some turn,
whether they failed or kept an earlier account, the next opens a fresh
conversation with the role's memory and briefing. This bound covers all recurring roles,
including program manager instances:

```yaml
execution:
  missing_reports_before_fresh_conversation: 3  # the default; zero also uses three
```

The value must be nonnegative. A recovered report resets the count, as does
replacing the conversation. The pass record and `yoyo sweeps` name the old and
new conversations and why the replacement happened. Existing conversations and
their events remain durable. See
[reading the passes](../operations.md#reading-what-the-recurring-tasks-found).

The development manager's continuation turns also carry the next slice of her
live docket, through the same listing as the first turn. Entries already
delivered on that pass are not repeated, and decisions and closed work are read
again before each slice. The harness continues while live entries remain
undelivered, even if a turn's account says complete. A reply without an account
must recover it through the one block-only request; otherwise the pass goes no
further. It keeps the account an earlier turn gave, recorded as partial, or
fails where no turn gave one. If the turn bound or a provider refusal ends the pass first, its
record counts the entries never delivered and names the oldest; the next pass
puts them ahead of entries already shown.

**A reply with more than one account keeps the last.** The contract is one
sweep block per reply, and a role that answers with two — a `more` and then a
`complete`, which is the shape the slip takes — has slipped rather than failed.
The last block is recorded as the pass's account, and the record notes beside
it that more than one was sent, rather than the pass being thrown away over
the shape of its reply after its decisions were taken. Everything else the
reply asked for is carried out as usual, and the role is told at the start of
its next turn that it wrote more than one, so it writes one next time. A sweep
block that cannot be read is still refused wherever it sits, and a reply with
one block is recorded exactly as before. Any other block the harness cannot
read is refused on its own and named on the pass's record beside what the pass
decided ([one bad block loses only itself](../conversation.md#one-bad-block-loses-only-itself)).

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
the [drain](runs.md#watching-instead-of-draining) is about the runs it hosts and not
about the schedule. Once it hosts none it starts no new pass — the session that
comes back takes it at its first pull, since the cadence is claimed durably —
and it waits for a pass still taking its turns before it restarts, until the
drain bound: a session restarting past the bound stops every pass still taking
its turns, and none is recorded as failed. One that an earlier turn gave its
account keeps it as partial, one with no account yet is recorded as a missed
pass, and either way the task is due again at once, so the session that comes
back tries the pass again at its first pull.

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
was stopped under its turn is recorded as one at once — tried again at once
where what stopped it was a restart into a new build — and one whose process
died carrying it, which records nothing, is recorded by the next pull that
considers the instance once the claim has stood five minutes with no ending and
no turn in flight on its conversation. See
[reading what the recurring tasks found](../operations.md#reading-what-the-recurring-tasks-found).

**The intake brake summons a development manager's task out of its cadence.**
The first enabled task whose role is `development-manager` is the one the
[failure-storm brake](runs.md#watching-instead-of-draining) fires the moment it trips,
whether or not the task is due: the same conversation, the same turn bound, the
same durable report, with the runs that blocked and the reason each blocked in
the message that wakes her ahead of the task's own prompt. It is a firing like
any other — it counts, it is charged to the session's `--budget`, and the
cadence runs on from it, so the hourly pass does not follow a summons a minute
later over the same ground — and `yoyo sweeps` shows it as summoned, naming
what tripped the brake. A project that schedules no such task gets no summons;
its brake is decided by the cooldown's probe rather than by her, and the hold
says so. The provider answering nobody refuses a summons exactly as it refuses
a scheduled firing, and the pause covers both. The summons is taken the way a
recurring pass is: beside the session's poll, which goes on pulling while she
answers, and holding her conversation so that no pass of hers is started into
it until the summons ends. While it is being taken the cooldown running out
starts no probe. A trip that finds a pass of hers, or any other turn this
session is taking, still in her conversation does not summon her into it — the
summons claims her task's firing before it asks her anything, so it would spend
that claim on a turn the conversation is held against — and summons her at the
first pull after that turn ends, provided the brake's hold still stands and has
not been escalated; a stopped run's delivery to her waits for her pass the same
way. A wake for a tracker block the harness refused is taken beside the poll
too, one at a time: a refusal in a conversation a pass or other turn of this
session is using waits for it to end and is woken for at the next pull after,
and one in a conversation another process is mid-turn with is refused before
anything is claimed and made at a later pass.

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
[`approvals.publishing: automatic`](publishing.md#publishing-without-automatic-integration), which is
the only value that pushes a branch or opens a pull request. Under `human`, the
other value, the harness opens no requests and reads no forge, so a request
somebody opened by hand in such a project is not noticed here.

**A pass of a role that owns documents is put the changes proposed to them.**
On every firing of a task whose role owns artifacts — the architect the designs,
specifications, and decision records; the Lead Product Manager the brief and
the goals — the harness reads the [proposed
amendments](artifacts.md#proposing-a-change-to-a-document-you-do-not-own) nobody has
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
[`yoyo sweeps`](../operations.md#reading-what-the-recurring-tasks-found). The reports
outlive the session that produced them and are written once and never revised.

### Working the report pile on a cadence

The other standing loop worth configuring is the one that drains the
[collected reports](../reporting.md#who-reads-them-and-what-became-of-each-one).
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
[the walk](../reporting.md#who-reads-them-and-what-became-of-each-one) — so each
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
[the reporting guide](../reporting.md#who-reads-them-and-what-became-of-each-one).
A project with no such task has none of the three.

### Working the amendment queue on a cadence

The third standing loop is the one that decides the [proposed
amendments](artifacts.md#proposing-a-change-to-a-document-you-do-not-own). A developer that
finds a design wrong proposes the change and carries on; the proposal waits on
the architect's decision; and until something woke her for it, that happened
only when somebody opened her conversation. This
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
      most ten a pass, and nothing else wakes you to decide them, so a queue
      nobody wakes you for is a queue nothing drains.
      Read the document each one names, and decide every one that is yours
      with the "amendment" action, approve or decline, with your reason. A
      change the wake says the operator decides is a change of fundamental
      intent: recommend approve, decline, or merge with another for it, with
      the reason, in the "recommendations" of your block. Deciding edits no
      document -- an approved change is then yours to make as a revision.
      Say in your summary how many you decided, how many you recommended to
      the operator, and how many wait behind them.
      When nothing is undecided, that is the report.
      Name every work item by what it is, with its identifier after it:
      "retiring the maintenance job (434.9)", never "434.9" on its own. An
      identifier alone is a defect -- nobody reading later knows the item.
```

What the pass produces is the architect's decisions on her own documents, and
recommendations on anything you decide. The harness puts the undecided
proposals in the wake, oldest first and at most ten a pass, and the batch closes
at the first proposal that would take the wake past its size bound. A proposal
she decides is put to her on every pass until she has decided it, whatever an
earlier pass said about it; one you decide that she already argued on an
earlier pass is not put again while it waits on you. Each decision she makes is
recorded on the proposal with the `amendment` action and said to you afterwards;
the firing's report in `yoyo sweeps` lists what she recommended.

**What she recommends to you reaches you once, as one decision list.** Each pass
that argued something you decide that is still undecided is a [finding that
needs your hand](../operations.md#where-a-finding-that-needs-your-hand-goes): it is
sent to you directly, tagged by member id, through the same operator-action
message every such finding uses, naming each proposal with what she recommends
and why, and it is not sent again. `yoyo status` names it on the needs-a-human
line — never folded into the count of things not named — until every proposal
in it is decided. A recommendation on a proposal its owner decides is in no
batch, because nothing about it is yours. Deciding the rest is yours, from the
same two verbs as before:

```sh
yoyo status                                     # each pass's batch waiting on you, and each undecided proposal
yoyo sweeps --task architect-amendments         # her reasons, pass by pass
yoyo amendment approve <id> --reason ...        # record the change as authorized
yoyo amendment decline <id> --reason ...        # turn it down, keeping why
```

A `merge` recommendation names the proposal it folds into; the record has no
merge of its own, so it is carried out as one approval and one decline whose
reason names the other. A proposal decided between her pass and your reading is
dropped from the batch wherever the batch is read — `yoyo status` derives it
from the sweep records and the amendment log together — so the batch is always
what you still have to decide rather than what she once said, and a batch whose
every proposal is decided stops being named. The same cadence works for the
Lead Product Manager over the product home, which is where the proposals you
decide arrive: changes to the brief or the goals that do not
say they are consistent with what those documents intend.

Whether the queue is draining is answered the way the pile's is: `yoyo status`
carries the undecided count and the oldest undecided proposal's age on every
reading, and names the queue on the needs-a-human line once that age passes a
week, as the move of whoever decides its oldest proposal — the owning role, or
you where that one is a change of fundamental intent. A queue this old says the
task is not keeping up, or is not enabled.

### A program manager instance's passes

A [program manager instance](setup.md#a-program-manager-instance) is woken the way a
recurring task is, by its own `triggers` block rather than by an entry here, and
its pass **is** a recurring-task firing: everything above holds of it unchanged.
The pause stops a pass and the intake hold does not; the claim is taken before
the first turn; an instance's pass is taken beside the pull and beside other
roles' firings, in the instance's own conversation, and where the bound on
firings in flight is reached it waits its turn by how long it has stood due like
a task's; a provider answering nobody is recorded as the wait; and every pass
ends in a durable record [`yoyo sweeps`](../operations.md#reading-what-the-recurring-tasks-found)
reads, filed under the instance's name — `yoyo sweeps --task reliability-pm` —
with the model its turns ran on. A recurring task named for a program manager
instance is refused when the file loads, because the two would share one
cadence and one record.

An instance whose lane is a standing goal audits the work that closed in its
lane against that goal on its passes, records each item it checked under
`audits` in its account, and admits a priority 0 correction for a violation
under the same bound and duplicate check the Lead Product Manager's sweep
works under; her sweep is handed those audits rather than repeating them
([the operations guide](../operations.md#reading-what-the-recurring-tasks-found)).
Which lanes are standing goals is the instance's remit to say: every instance's
pass message carries the audit's contract and tells an instance in such a lane
to use it, and the program manager persona says so too, both the copy this
repository's instances read and the one `yoyo init` ships.

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
found](../operations.md#reading-what-the-recurring-tasks-found) says how the record
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

**A pass may be asked for.** Any instance may be asked for a pass, and one with
no `triggers` block passes only when asked — never on a timer and never because
work landed. Two people ask, and nobody else: the operator, with
`yoyo agent pass <agent>`, and the Lead Product Manager, from her conversation
(another role that wants a pass asks her). A request may name a focus — one area
the instance's remit lists, or up to ten repository paths — which the pass
inspects in place of the next area in its rotation; `--focus` takes a value
naming a file or directory in the repository as a path and anything else as an
area, and is repeated for each path. Each request is appended to
`projects/<product id>/state/program-managers/<agent>/requests.jsonl` with who
asked and when, and the scheduler takes it at its next pull under every rule
above: the pause holds it, a turn in flight on the instance's conversation goes
first, and a provider answering nobody leaves it waiting with that reason
written on it. At most one request per instance is open; one made while another
is open is recorded as joined to it, its focus is added to the open one's, and
the one pass answers both. The request is recorded as taken, naming the pass,
before that pass's first turn; the pass's record in `yoyo sweeps` names the
request and who asked, and the pass closes the request, completed or failed — a
failed one is not retried, and whoever asked may ask again. A request is never
dropped for age, and lives in that file rather than in the scheduler, so one
waiting when the scheduler restarts is taken by the next one, and one whose pass
a stopped scheduler was carrying is taken again — unless a turn is in flight on
the instance's conversation, which means another process is still carrying it.
`yoyo status` lists each instance's requests — every open one and the latest
five of the rest — as waiting, taken, joined, completed, or failed, with when. A requested pass is not held to the `5m` minimum, and it moves the
cadence as an events pass does.

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
  [kept outside the repository](setup.md#keeping-the-configuration-outside-the-repository)
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
[configures an instance](setup.md#a-program-manager-instance), which binds
`personas/program-manager.md` rather than writing its own. Like the rest of the
persona, it says how the role works and grants nothing — the role's contract and
the lane decide what an instance may do.

Every shipped persona and role contract carries the rule that the role
applies the standing goals to everything it writes and every decision it
makes, whichever goal the work item or its lane serves. The standing set is
read from the goals documents' `Standing goals` section under the configured
`product.specifications` home, delivered as authoritative product intent;
in Yoyodyne it is [the plain-language and autonomy goals](../product/goals/v1-goals.md#standing-goals).
An output or decision that breaks one is a defect to report, naming the goal
and where it was broken. The pass prompts `init` writes carry the same rule,
and a program manager applies it to its lane report and post-mortems too.
Every program manager remit this repository's instances read, under
`.yoyodyne/remits`, carries the rule as well, naming the lane report and,
for the factory-flow remit, the post-mortems on stopped runs; no remit is
shipped in the template, and a test fails if any of them lacks it.

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
