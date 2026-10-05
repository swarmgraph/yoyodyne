# Provider plugins

Yoyo runs agents through a provider — a coding CLI or a harness that speaks to a
model API. Two are in the vocabulary and this build ships an adapter for both:
Claude Code and Codex, which can serve every role. Developers use a worktree-write
sandbox. Reviewers and management roles use read-only access: Claude Code refuses
all tools, while Codex permits inspection under its native read-only sandbox with
network access, escalation, and external integrations disabled
([capability validation](#capability-validation)).

The recorded codex-cli 0.159.2 streams cover session startup, reconnect notices,
a provider reply, token usage, and a completed turn. Failed turns and shell,
patch, and tool items still lack recorded live streams; see
`internal/backend/codex/testdata/streams/README.md`. Separate bounded local probes
of that CLI version verify read-only launch settings and native resume against a
mock provider, configuration isolation against a fake MCP server, and native
sandbox refusal of a file write and a localhost network connection. A mock
provider also issued a real Code Mode call: a nested shell read succeeded and
a nested file write was denied before the CLI completed its reply. Those probes
do not certify every future CLI version or constitute a live model review.

A project can declare a provider of its own in its configuration, without forking
this repository or rebuilding the binary. **What a declaration supplies is the
dialect and the executable, not a new way of launching a process.** Your provider
runs on a compiled adapter — Claude Code's or Codex's — which starts it and reads
its stream, and your declaration says which executable that adapter runs and how to
read what it says about rate limits, retries, and reset times. So a fork of a
provider yoyo already speaks, a proxy in front of one, or anything that talks the
same protocol and reports its limits differently is reachable from configuration
alone. Something that speaks a different protocol needs an adapter, which is a
change to yoyo.

A declaration that names no adapter, or one this build does not ship, is refused
when the configuration loads. There is deliberately no way to declare a provider
that validates and can never run.

This document is what a provider plugin is, what it may and may not decide, and
how one is written.

## What yoyo needs from a provider

Not very much, and deliberately so. A provider says a great many things while a
run is going; almost all of it is prose, tool calls, and accounting, and yoyo
acts on none of it. What it has to know is which of nine things just happened.

| Answer | What it means |
|---|---|
| `served` | The attempt was served, or a limit that was refusing is refusing no longer |
| `retrying` | The provider hit something transient and is retrying by itself |
| `limit-reached` | A usage limit is refusing work and will lift — with, if the provider said so, when |
| `unavailable` | The provider's own servers could not serve the attempt, transiently |
| `interrupted` | The attempt died of something that judged nothing about the work |
| `model-unavailable` | The provider has not got the model this attempt asked for |
| `unauthenticated` | The provider will not accept the account the attempt was made under; a person has to log in |
| `unreachable` | Nothing answers at the provider's API; the network has to come back |
| `refused` | A refusal that stands; the same request earns the same answer |

Those nine are the contract. Everything yoyo does about a provider refusing work
— parking a run, recording the deadline, probing, blocking when the wait no
longer fits — is driven by them and by nothing provider-specific.

The distinctions are narrower than they look, and each one was learned from a
run that went wrong without it:

- `retrying` is not `unavailable`. A provider retrying by itself has not ended
  the attempt and nothing about the account is exhausted. By the time it reports
  an overload it has usually already spent its own retries.
- `unavailable` is not `limit-reached`. Nothing about the account is exhausted
  and no reset time is ever quoted, so it cannot be folded into the case below:
  an exhausted limit polls on an interval measured in tens of minutes, and an
  overloaded server lifts in seconds.
- `interrupted` is not `refused`. A connection that went away mid-reply says
  nothing about the request. Reading it as a judgement of the work fails a whole
  run on weather.
- `model-unavailable` is not `refused`, though it is a refusal that stands.
  What separates it is that the caller has an answer to it: a different selector
  is not the same request. It is what makes an optional pinned model version safe
  to name — a pin the provider has retired falls back to the family alias the
  agent already names rather than stopping the agent — and folding it into the
  answer above would throw away the one thing that makes it actionable. Nothing
  about the account is exhausted and no reset time is ever quoted, so it is not a
  limit either.
- `unauthenticated` and `unreachable` are not `refused`, and not `interrupted`
  either. A login that expired earns the identical answer on the next attempt,
  so relaunching into it spends a run's whole relaunch budget on nothing; but
  failing the run over it costs a finished change for a login the operator
  renews in a minute. So both are [a wait that spends nothing](operations.md#waiting-out-a-provider-nobody-can-reach):
  no relaunch, no repair attempt, no blocker, ended by the provider answering
  again. They are two answers rather than one because the operator is told two
  different things — log in, or wait for the network — and nothing else about
  them differs. A dialect that cannot tell them from a mid-reply drop should
  leave the drop as `interrupted`: that one reached the provider, and a relaunch
  is right for it.

## Reset times: the two cases the contract owns

A plugin reports the reset time its provider named, and nothing more. What that
time is *worth* is yoyo's answer, not the plugin's, and it is the same answer for
every provider:

- **No reset time is unknown, not fatal.** A provider can refuse work without
  saying when it will stop — the monthly overage allowance does exactly this,
  while the ordinary rolling window keeps resetting on its usual schedule. Yoyo
  waits `execution.usage_limit_unknown_reset_pause` (thirty minutes by default)
  and asks again, under the same budget as any other wait.
- **A reset time that is not in the future is malformed and is not trusted.** A
  limit still refusing work while naming a reset that has already passed is not
  describing a wait; honoring it would reissue straight back into the same
  refusal with nothing bounding the attempts. Yoyo stops the run and records a
  blocker, because a clock skew or a window the provider has not rolled yet is a
  fact for a person.

A reset time your plugin cannot read is the first of these, not an error: the
limit still reaches yoyo as a limit. What yoyo refuses is guessing the wait, not
noticing the refusal.

See [waiting out a provider usage limit](operations.md#waiting-out-a-provider-usage-limit)
for what the waiting itself looks like.

## A plugin describes; it never decides

There is nowhere in a plugin to write a duration, a retry count, a budget, or a
condition on any of them. This is not a convention — the format has no such
field, and a configuration that tries to state one fails to load.

The reason is that whether to wait, how long, and against which budget are what
your `execution.usage_limit_*` settings mean and what a run's safety properties
rest on. A plugin that could decide to keep waiting could spend an account.

## How a plugin is delivered, and what that costs

A plugin is **data in your project's configuration**, and its dialect is a list
of ordered rules. Three deliveries were possible and this is the one chosen, so
here is the trade rather than the assertion.

**Declarative rules in configuration — what ships.** No fork, no vendoring, no
rebuild, and nothing on the other side of the boundary that runs, so there is no
new trust boundary to defend and the "describes but never decides" property is
structural rather than asked for. What it costs is reach: it covers a provider
whose reports differ from a built-in's in spelling — field names, event names,
the unit a reset time arrives in, a limit announced in a sentence — and it rides
on an existing adapter, so it covers nothing about how the process is started.

**Compile-time registration — what the built-ins use, and the only way to add an
adapter.** A dialect written in Go can read a shape no rule can describe. The
built-in Claude Code dialect uses it for exactly that: telling a subagent's
completion apart from the invocation's own terminal, and telling a transient 529
apart from a 4xx that describes the request. Launching a provider is the same
kind of thing — a command line, a permission model, a stream format — and stays
compiled in for the same reason. What it costs is that a user has to fork or
vendor yoyo, which is why the dialect and the executable were pulled out in front
of that boundary.

**A subprocess speaking a documented protocol — considered, not built.** It keeps
users independent the way declarative rules do *and* lets them run arbitrary
code. What it costs is a boundary to defend, and the thing on the other side is
untrusted: a protocol, a timeout, a crash policy, and a resource bound, all
guarding a component that gets a say in whether a run waits. That is a real piece
of engineering and it is not worth it until a plugin exists that declarative
rules cannot express. If you have one, that is the case for building it.

**What a plugin does not do:** it does not launch the provider. Starting the
process, sending the prompt, scoping the tools, and reading the stream are the
compiled adapter's, and a declaration names which one does that for it. So a
declaration reaches a provider that speaks a protocol yoyo already speaks; a
provider that speaks a different one needs an adapter written in Go, which is a
change to yoyo rather than to your configuration.

`yoyo doctor` diagnoses a declared provider as what it actually runs on: it looks
for the executable the declaration named, and asks that provider's own adapter
whether it is installed, missing, or unauthenticated, the same way it reports a
built-in. A backend nothing in this build can launch — a declaration that would
not load, or a name nothing describes — is reported as one this build has no
adapter for, with the configuration as the remedy, because nothing you could
install would give this build one.

## Capability validation

A declared provider states which roles it serves and which kinds of tool access
it can hold them to. Both are checked when configuration loads, before any work
is assigned. The same check applies to built-in providers.

The two kinds of tool access are:

- `read-only` — the agent may reason over supplied evidence and inspect the local
  repository without changing it. Every role but the developer needs this.
  Claude Code enforces the stricter empty-tool variant; Codex permits native
  read-only inspection and disables tool network access and external integrations.
- `worktree-write` — the agent's work is editing a worktree, and the provider
  must be able to scope writes to it. The developer needs this.

A provider that declares only `read-only` is refused for a developer agent, and
one that declares only `worktree-write` is refused for a reviewer. A declaration
cannot change the launch policy enforced by its compiled adapter.

Both built-ins declare both kinds of access. For read-only roles, the Codex
adapter fixes `--sandbox read-only` and `approval_policy="never"` on fresh and
resumed invocations. It ignores user configuration and execution-policy rules,
launches from an empty temporary directory outside the repository, and supplies
the repository's absolute path in the prompt. Project configuration cannot add
MCP servers through the inspected repository. The adapter disables apps, plugins,
hooks, browser and computer use, image generation, automatic skill dependencies,
subagents, and other external integrations. Authentication remains in the
provider's own `CODEX_HOME`; the harness does not copy credentials. Read-only
invocations ignore that account's `config.toml`, so custom model endpoints defined
only in that file are unavailable even though native authentication files remain
in use. Native session
IDs and resume remain in use, with the launch policy reapplied each turn.

This is a write and network boundary, not confinement of reads to an evidence
bundle or to the repository. Codex may inspect other locally readable files.
Its model-visible Code Mode tools may remain available. The CLI may write its own
session and authentication state; the read-only policy applies to agent execution.
Managed organizational configuration remains an installation authority and must
be compatible with the adapter's restrictions. The installed 0.159.2 exec command
has no supported Plan-mode switch, so the harness supplies an analysis-only
instruction rather than claiming native Plan mode. An installation that rejects
the required flags or settings fails the invocation; the adapter does not retry
with weaker permissions.

The same check stands behind a substitution. When a turn is moved off the model
it asked for — because that model's capacity window closed — the endpoint it
would be moved onto is checked against the tool access the role requires, and a move
onto a provider that cannot hold it is refused with the tool access named. The turn
then takes the refusal it would have taken anyway rather than being served
somewhere the configuration would never have permitted. Configuration validation
answers for the configuration as written; this answers for the endpoint an
invocation is actually about to be made on.

A provider you declare can be the alternate as well as the agent's own, which is
what an agent's `failover.provider` names — see
[configuration](configuration.md#serving-a-turn-from-a-permitted-alternate-model).
That is worth knowing because a crossing costs something a substitution within one
provider does not: your provider holds no session for the conversation, so it is
handed the conversation rebuilt from the harness's durable record rather than a
session identifier to resume. Nothing about your declaration has to say so and
nothing about your adapter has to do anything differently — the harness sends no
session and assembles the context — but the first turn your provider takes for a
conversation somebody else was holding is a long one, and it is a first turn
rather than a resumption.

An endpoint is the provider, the version of the adapter that reaches it, the
account alias, and the model, and every cost line records all four. A declared
provider's records therefore name your provider *and* the adapter version this
build read its stream with, which is what lets a later reader tell two harness
builds reading one provider differently apart. You do not write the adapter
version: it follows from the adapter your declaration names.

The tool access is also what decides the session mode an invocation is made in, and
the invocation the harness asks for carries none: nothing above the adapter names
a mode, so which one a role gets follows from which role it is. That matters most
for what an adapter must *not* choose. A provider with an interactive planning
mode puts that mode's own workflow into the session — do not execute yet, write a
plan, hand the plan back — and a harness-invoked role receives it on top of a role
contract that says the opposite: a reviewer told to plan when its contract wants
one verdict, or a developer told not to edit when the whole run is an edit. An
adapter picks the mode that grants what the tool access needs and nothing else, never
the one that instructs.

## Codex effort

A Codex agent that sets an effort passes it explicitly with
`--config 'model_reasoning_effort="high"'` for `effort: high`. The override is
applied before `resume`, on initial and resumed invocations of every role.
An omitted or empty effort passes no effort override at all, so the Codex CLI
resolves the level from its own configuration. Yoyo does not read the account's
Codex home to discover that level. Read-only roles still ignore user configuration
for isolation, as described above; their level comes from the configuration
Codex resolves under those restrictions. Explicit effort overrides Codex settings.

The accepted levels are model-specific. In codex-cli **0.159.2**, the bundled
catalog advertises `low`, `medium`, `high`, `xhigh`, `max`, and `ultra` for Astra,
Sol 6.1, Sol 6, Sol 5.6, Terra 5.6, and both Daybreak selectors. Luna 6, Luna 5.6,
and `codex-auto-review` stop at `max`; `gpt-5.5` stops at `xhigh`. Defaults are
`low` for Astra, Sol 6.1, Sol 5.6, and Daybreak Blue, and `medium` for the rest.
The [configuration guide](configuration.md#an-agents-effort-level) lists exact
selectors. Unlisted selectors and unsupported levels are refused at load;
refusals name accepted levels where the model is established.

These values were established locally from `codex debug models --bundled`;
`codex exec --help` and `codex exec resume --help` establish the config flag.
Local configuration validation confirms a string value is required, but accepts
unadvertised strings too, so Yoyo checks the model catalog rather than relying
on the CLI parser to refuse them. The recorded catalog is
`internal/backend/testdata/codex-cli-0.159.2-effort.json`. No provider call or
operator Codex home was needed to establish it. These are Codex's own values,
not a translation of Claude Code levels, and a declared Codex provider inherits
the same policy.

Requested effort is recorded beside the requested model, and stays empty when
no override was passed. Codex records also carry `effort_description`, with
`provider_*` and `review_*` names on run records: `high, from the agent`,
`high, from the Codex configuration` when the stream reports an unrequested
level, or `not reported, from the Codex configuration` when it does not.
Run notes, status, and dashboard cards use that description. Reported effort is
separate: `effort_reported: false` says the served effort was not reported.
The current `exec --json` stream normally omits it; a `session_configured` event
with `reasoning_effort` supplies it. For a live check, run one harness invocation
at `codex/gpt-6-astra/high` and inspect its Codex session JSONL `turn_context`
line: `payload.effort` and
`payload.collaboration_mode.settings.reasoning_effort` should both be `high`.
Deploy this support before activating live configuration that requires it.

## Writing one

Providers go under a top-level `providers:` key in your configuration, keyed by
the backend identifier your agents will name. See
[the configuration guide](configuration.md) for where that file lives.

```yaml
providers:
  my-harness:
    # Which compiled adapter launches it and reads its stream. Required, and
    # `claude-code` is the only one this build ships.
    adapter: claude-code
    # The executable that adapter runs. Omit it for the adapter's own.
    binary: my-harness
    roles:
      - developer
      - reviewer
    postures:
      - read-only
      - worktree-write
    capabilities:
      structured_events: true
      session_resumption: true
      structured_output: true
      tool_control: true
      local_auth: true
    dialect:
      rules:
        # Rules are tried in order and the first match wins, so a narrower
        # reading goes in front of a broader one.
        - answer: retrying
          type: retry

        # A limit an overage allowance is already serving is still serving.
        - answer: served
          type: quota
          fields:
            using_overage: "true"

        - answer: limit-reached
          type: quota
          fields:
            state: exceeded
          kind_field: window          # the provider's own name for the limit
          reset_field: resets_at
          reset_format: unix-seconds

        # Any other quota report is capacity.
        - answer: served
          type: quota

        - answer: unavailable
          terminal: true
          failed: true
          match: '(?i)\b503\b'

        - answer: interrupted
          terminal: true
          failed: true
          match: '(?i)connection reset'

        # A model this provider has not got. Narrower than the refusal below,
        # so it goes in front of it.
        - answer: model-unavailable
          terminal: true
          failed: true
          match: '(?i)unknown model'

        # Anything else that ended badly is a refusal that stands.
        - answer: refused
          terminal: true
          failed: true

agents:
  developers:
    role: developer
    backend: my-harness
    model: my-model
```

### Provider fields

| Field | Meaning |
|---|---|
| `adapter` | Required. The backend whose compiled adapter launches this provider. `claude-code` and `codex` are the ones this build ships; naming anything else is refused at load. |
| `binary` | The executable that adapter runs. Omit it for the adapter's own. |
| `roles` | Which of the harness's roles this provider serves. |
| `postures` | The tool access it can hold its roles to: `read-only`, `worktree-write`, or both. |
| `capabilities` | What the provider can do, stated rather than assumed. |
| `dialect.rules` | How to read what it says, below. |

### Rule fields

| Field | Meaning |
|---|---|
| `answer` | Required. One of the nine answers above. |
| `type`, `subtype` | The provider's own names for the event, matched exactly. |
| `terminal`, `failed` | Whether the event ends the invocation, and whether it ended badly. |
| `match` | A regular expression the event's prose must contain. |
| `channel` | Where the event was said: `envelope` for the provider's stream, `stderr` for what its process wrote to stderr, `stdout` for plain text it wrote to stdout before any envelope. Omitted is `envelope`, so a rule that says nothing reads the stream and never a process's prose. |
| `fields` | Dotted paths into the event payload that must equal the given value. |
| `kind`, `kind_field` | The provider's own name for the limit, stated or read from the payload. `limit-reached` only. |
| `reset_field`, `reset_match` | Where the reset time is: a payload path, or a regular expression over the prose with exactly one capturing group. `limit-reached` only. |
| `reset_format` | `unix-seconds`, `unix-millis`, or `rfc3339`. Required whenever a reset time is read. |

A rule that states no condition at all is refused, because it would answer for
every event the provider emits. So is a rule that reads a reset time without
saying how to read it: a number with no unit is not a time, and guessing the unit
is how a five-hour wait becomes five days. And so is a `stderr` or `stdout`
rule with no `match`: prose carries no type, no subtype, and no payload, so the
expression is the only condition it can have, and without one the rule answers
for whatever the process happened to say.

Each plain channel is handed to a dialect as one event, and only when the
process ended without a terminal of its own: stderr first, and the plain text
the process wrote to stdout before any envelope only when stderr answered
nothing. They exist for the refusal a CLI makes before it writes anything
structured — a login it will not accept, an API nothing reaches — which both
built-in dialects, Claude Code and Codex, read as `unauthenticated` and
`unreachable` and nothing else. A declared dialect reads either only through a
rule that names the channel:

```yaml
        - answer: unauthenticated
          channel: stderr
          match: '(?i)not signed in'
        - answer: unauthenticated
          channel: stdout
          match: '(?i)not signed in'
```

A terminal the provider did write is never second-guessed by what it said as
prose, so a rule on either plain channel cannot change the answer to any
invocation that ended the way the provider ends one. Which channel an
`unauthenticated` or `unreachable` answer came on is recorded beside the wait it
earned — on the run and on the product's outage record — as evidence rather
than as anything the harness acts on. A refusal read off `stdout` is the
provider having written prose where its stream should have been, which on the
Claude Code adapter is also a stream that would otherwise have failed to
decode: the decode error is not the invocation's failure when the lines that
failed to decode were the refusal.

A field the provider omits is *absent* rather than false, and a rule matching
something absent matches nothing. That is the safe direction — what it costs is a
refusal yoyo keeps failing on, and what the other direction costs is a wait
nobody can justify.

### What is not expressible

A reset time quoted in human local time — `resets 8:30pm (America/Los_Angeles)` —
cannot be read by a declarative rule today, because none of the three formats
covers a wall-clock time plus a zone plus an implied date. A limit announced that
way is still reported as a limit; its reset time is simply unknown, so yoyo polls
on the interval rather than waiting to a deadline. If your provider only ever
states reset times that way, say so — it is the clearest case for a fourth
format.

## What a new adapter owes

An adapter is Go code and a change to yoyo rather than to a configuration, and
what it owes before it can be called complete is one suite: every adapter
classifies the same provider conditions the same way.

The conditions are named once, in terms no provider owns — capacity exhausted, a
model the provider has not got, an account it will not accept, a network
failure, and a refusal that judged the work rather than the environment — and
each adapter supplies its own provider's words for each of them. What the suite
asserts is not that the words match, which they never will, but that the harness
is left holding the same answer whichever adapter met the condition: a window to
wait for, another model to ask for, another attempt to make, or a refusal that
stands.

Two things make it a gate rather than a checklist. It puts the samples through
the adapter rather than the dialect, so an adapter that reads a condition
correctly and loses it on the way to the result fails just as one that misreads
it does. And an adapter this build ships with no cases beside it fails by being
absent, so a new adapter arrives as a failing test naming what it owes rather
than as a provider nobody ever asked how it reads a refusal.

A declared provider is not a new adapter and owes nothing here: it rides on the
adapter it named, and its rules are checked where your configuration loads. What
the suite protects for you is the adapter underneath it — a dialect this build
ships and yours reads a condition differently is exactly the divergence it
exists to catch, and the case sets are where a provider's real words are written
down.

## Where the contract lives in the code

`internal/backend/contract.go` is the contract itself: the answers, the
observation a dialect returns, and `ReadReset`, which is the single place the
unknown and past-reset cases are decided. `internal/backend/declarative.go` is
the rule format on this page. `internal/backend/registry.go` holds the built-in
descriptions and turns a declaration into one. `internal/backend/claudecode/dialect.go`
and `internal/backend/codex/dialect.go` are the two built-in dialects, each one
implementation of the same contract and neither given special treatment above it
— the adapter beside each takes whichever dialect it is handed, which is how a
declared one comes to read a real stream.
`internal/backend/conformance` is the suite above: the conditions, the answer
each one must leave the harness holding, and every shipped adapter's own words
for them.
`internal/cli/provider.go` is where the backend an agent named is resolved into
the adapter that runs it.
