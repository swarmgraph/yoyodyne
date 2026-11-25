# One pane of glass: supervision, residency, and the brake in conversation

Drafted for yoyodyne-ifd.130.1. **Status: draft — the architect's voice and the
operator's approval are both outstanding.** It lives outside the artifact homes
for the same reason [the documentation map](docs-map.md) and [team mode
scope](team-mode-scope.md) do, and for one more that is specific to it.

The shared reason is the contract: every document inside an artifact home
carries identity frontmatter — id, kind, status, what it supports, a revision
log — and a document without an owner's hand on it cannot honestly carry a
revision log saying who wrote it. The specific reason is that ifd.130.1 was
admitted precisely so this architecture would be *"reviewed with her rather than
shaped by the product manager alone"*, and `docs/designs/` is a protected path
this work item grants no exception to. A developer writing there on its own
authority would satisfy the item's form and defeat its purpose in the same
stroke.

So this is design **input**, in the shape the architect can adopt: it decides
nothing that is hers to decide, and when she takes it up it moves to
`docs/designs/` under her identity, or is replaced by what she writes instead.
Everything below is grounded in the code as it stands — file and line references
throughout — so that what she is deciding is which shape to take, not what
exists today.

**This document does not satisfy yoyodyne-ifd.130.1's done condition, and is not
offered as though it did.** That condition is a design recorded in the
architect's home whose sections the epic's implementation children can cite. Two
things are missing and neither is a developer's to supply: the item grants no
`protected-path grant:` for `docs/designs/`, and four of the questions below are
the architect's judgement, which is the whole reason the operator asked that this
route through her rather than being settled by anyone else. What this run can
honestly deliver is the grounding and the options, and a clear statement of what
is already decided and by whom — which is what follows. The item is escalated as
not completable in this form; [what is settled and what
is not](#what-is-settled-and-what-is-not) says exactly what is missing.

## What this is for

Three residents run today and none of them knows about the others: `yoyo chat`
(`internal/cli/chat.go`), the scheduling loop `yoyo work --watch`
(`internal/cli/schedule.go:48`), and the Slack sink `yoyo slack`
(`internal/cli/slack.go`). An operator who wants the full loop starts three
terminals and remembers which. The goal this serves — *the human's routine
interface is the product manager* — is not met by an interface you have to
assemble yourself every morning.

## 1. The target shape, and today's as the step toward it

The end state, in the operator's own framing: the whole product-manager flow
runs headless under launchd, and `yoyo chat` opens a thin connection to it. The
service is always running; you connect to it or leave it working unattended, and
you attach and detach at will.

That reframes launchd rather than eliminating it. launchd returns as the
supervisor **of the product-manager service itself**, not of the pieces — not of
the sink, not of the scheduling loop. One managed thing, which supervises its
own residents.

Today's shape is the interim step: `yoyo chat` is the process that spawns and
supervises, and detaching means exiting. **What carries over is the coordination
model, and it is the reason the interim step is not throwaway work.** All three
residents today coordinate exclusively through the runstate store — the intake
hold (`internal/runstate/intake.go`), watch-session transitions
(`internal/runstate/watch.go`), conversation records
(`internal/runstate/conversation.go`), triage counters
(`internal/runstate/triage.go`) — and never through each other. No resident
holds a handle on another; each reads and writes durable files.

That is what makes the later move a **re-parenting rather than a rewrite**. When
the PM becomes a launchd service, the supervision code changes parent and the
residents do not change at all, because nothing in them ever addressed their
parent. Two things must be true for that to hold, and the interim step should be
built to keep them true:

- **No resident may depend on being spawned by a chat.** A resident learns what
  it needs from the store and its configuration, never from an inherited handle,
  file descriptor, or argument that only a chat would know to pass.
- **Attach and detach must already be distinct from start and stop** in the
  interim, even though today they coincide. If the interim conflates them, every
  call site that assumed "chat exit means sink exit" is a site the service
  migration has to find.

## 2. The supervision tree

`yoyo chat` becomes the supervisor of two residents: the scheduling loop and the
Slack sink. It supervises them as child processes rather than as goroutines, and
that is the load-bearing choice of this section.

The reason is the credential boundary, and it is not a preference.
`docs/designs/slack-reporting-design.md` decision 2 states that **no run
process, and therefore no agent's subprocess tree, ever has a Slack token in its
environment at all**. Today that is structural: `yoyo slack` is started by the
operator and reads `SLACK_BOT_TOKEN` and `SLACK_APP_TOKEN` from its own
environment (`internal/cli/slack.go:34-37`), and nothing that spawns an agent
ever holds them.

**Supervision puts that guarantee at risk, and the design has to say so out
loud.** A supervising chat must hold both tokens in order to pass them to the
sink — and the same chat process spawns the product manager's provider
subprocess through the Claude Code backend (`internal/backend/claudecode`, wired
at `internal/cli/chat.go`). Absent an explicit rule, the PM's subprocess
inherits the supervisor's environment and the tokens with it. The letter of
decision 2 survives, because a chat is not a run process. Its purpose does not.

The rule this design proposes, and asks the architect to ratify:

> The supervisor passes the Slack tokens to the sink child and to nothing else,
> by constructing that child's environment explicitly rather than by inheriting
> its own. Every other child the supervisor spawns — the provider subprocess
> above all — is spawned with an environment from which both token variables
> have been removed. The boundary stays structural: it is a property of how each
> child is constructed, not of what any agent was asked not to do.

Beyond that, supervision is deliberately thin. The supervisor starts each
resident, notices when one exits, and restarts it under a bounded policy; it
does not proxy their work, hold their state, or sit between them and the store.
A supervisor that only starts and watches is one the launchd migration can
replace wholesale.

## 3. What the chat owns, and what survives it

The residency question does not have one answer, and that is the substance of
this section rather than a complication in it. The two residents differ in what
their death costs.

**The scheduling loop's in-flight runs must survive the pane.** A run is not a
thing the chat owns: it has a claimed work item, a worktree, a lease, and
durable state. Closing a terminal is not an instruction to abandon any of that,
and an operator who lost an hour of development by pressing Ctrl-D would learn
to never close the pane — which is the opposite of attach-and-detach-at-will.

**The sink may die with the pane, and losing it costs nothing durable.**
Reporting is observation and never a gate (`slack-reporting-design.md`
decision 3): the sink tails durable records from per-stream cursors, so a sink
that stops and returns catches up rather than losing anything. Its death costs
latency in a chat channel, which is recoverable by definition.

So the proposed rule, per resident:

| Resident | On chat exit | Why |
|---|---|---|
| Slack sink | **Dies with the pane** | Stateless against durable cursors; catches up on return; holds the credentials, which should live no longer than the supervisor that granted them |
| Scheduling loop | **Survives, detached** | Owns claimed items and in-flight runs; killing it either abandons work or forces a park nobody asked for |

That asymmetry means "the chat supervises its children" cannot be a single rule,
and the design should name the two policies rather than implying one.

The loop surviving raises the question of what supervises it once its parent is
gone, and the honest interim answer is: nothing, until the PM service exists.
A detached loop in the interim is an orphan that keeps running and keeps
recording its watch transitions, which `yoyo status` and the sink both read
(`internal/runstate/watch.go:55-81`). That is acceptable because it is exactly
what a separately-started `yoyo work --watch` is today — the interim step does
not make it worse — and it is one of the things the service tier fixes, which is
an argument for the target shape rather than against the interim one.

## 4. Reattachment, and the conversation-lease seam

A returning chat reattaches by reading the store, because the store is where
every resident already says what it is doing. It finds the scheduling loop
through the watch-session transitions, the sink's absence by its absence, and
the conversation through the conversation record.

The conversation is the hard part, and it is the seam this section exists for.

`ConversationStore.Hold` (`internal/runstate/conversation.go:339`) takes an
advisory `flock` (`internal/runstate/lock_unix.go:17`), **per agent rather than
per role**, released by the operating system when the holder dies. Its own
comment gives the reason: *"Two processes resuming one provider session would
interleave their turns and overwrite each other's record of them."*

That reason is exactly right, and it is narrower than the lock. It is a
constraint about **resuming a provider session** — about who may take the next
turn. The operator's requirement is that the console and the operator's
assistant both *reach* the product manager, and most of what reaching means is
reading: what is pending, what was decided, what the PM is waiting on.

So the proposed seam splits the two, and the single-holder lock stays exactly as
it is for the half it was written for:

- **Taking a turn** — invoking the provider and appending the turn to the record
  — continues to require the exclusive lease, unchanged. One holder, and the
  second caller is refused with `ErrConversationHeld` as it is today. Nothing
  about provider-session correctness is loosened.
- **Reading the record**, and **submitting a message for the holder to take up**,
  require no lease. A reader opens the conversation file; a submitter appends to
  a queue the holder drains on its next turn.

The lease is already the right reattachment primitive for the first half, and
for a reason worth stating rather than relying on: because it is an advisory
file lock, a chat that was killed leaves **no stale lock to clear** — the OS
drops it — so a returning chat finds the conversation immediately available.
That property is why reattachment needs no lease-breaking command, no timeout,
and no recovery path, and the design should record it as a dependency rather
than an incidental.

## 5. The brake, escalated into conversation

Most of this machinery exists; what is missing is one path between two halves
that already work.

The half that exists on the placing side: a failure storm places the same intake
hold an operator places (`internal/orchestrator/schedule.go:172`,
`ScheduleBrake`), at `execution.blocked_runs_before_intake_hold`. The session
then sits in `WatchBraked`, and the reason distinguishes an operator's hold from
the brake's own (`internal/runstate/watch.go:67-71`). The hold carries a
`Reason` (`internal/runstate/intake.go:53`), and triage counters record durably
what each item has already been given (`internal/runstate/triage.go`).

The half that exists on the answering side: a conversation can already hold and
release intake (`internal/chat/intake.go`, `Hold`/`Release`/`Held`).

**What is missing is the raise.** Today a braked line is a state an operator
discovers; the design makes it a decision the product manager brings to them,
with the triage record and a recommendation attached.

The flow, and the constraint on it stated in the same breath:

1. The brake places the hold, as it does now. **Nothing here changes**, including
   that the brake cannot lift its own hold — `schedule.go:174-181` is explicit
   that nothing in that package releases a hold, *"because what a held queue
   needs is a person, which is the whole reason for tripping it."*
2. The product manager notices the hold and that the brake placed it, from the
   hold's recorded reason, and raises it in the conversation with what it has:
   which runs blocked, what triage has already spent on them, and a
   recommendation.
3. **A person answers.** The recommendation is a recommendation. Releasing
   intake is the operator's act, taken through the conversation's existing
   `Release`, and the product manager must not be able to clear a brake by
   agreeing with itself. This is the point of the escalation and the thing most
   easily lost in implementing it.
4. **ifd.129's terminal-side release stays the path that works with no chat
   open**, unchanged and not deprecated. A brake that could only be answered in
   conversation would be a brake that traps an operator whose chat will not
   start — which is the failure mode most likely to coincide with a storm.

## 6. The 125.3 residency collision, resolved

Two answers to one residency question were in flight: yoyodyne-ifd.125.3's
start-on-login sink service, and this epic's chat-supervised sink. They do not
compose — they are two owners for one process — and the operator has already
decided between them.

**The product manager supervises the sink. 125.3's service tier is removed.**

The reasoning, recorded so 125.3's implementation does not have to reconstruct
it: a start-on-login service for the sink alone is the right answer to the wrong
question. It makes one resident permanent while the thing that gives it
something to report is still a terminal an operator has to remember to open.
Under the target shape in §1, the permanent thing is the PM service, and the
sink is one of its residents — so a separate launchd job for the sink would be a
second supervisor to unwind at exactly the moment the first one arrives.

This must reach 125.3 **before** it builds the service, and nothing in this
document carries it there. A developer may not admit work to the backlog or
amend another item, so the propagation runs through the product manager: the run
that drafted this named the 125.3 amendment in its summary as work to admit, and
until the product manager acts on it, 125.3 still has a service tier the
operator has removed. Whoever picks 125.3 up should read this section first and
stop if the item still describes a start-on-login sink service.

The drafting run could not confirm the state of 125.3 itself — the tracker was
locked for its whole session (`openat LOCK: operation not permitted`) and
`.beads/issues.jsonl` is a passive export that stops at ifd.99 — so this is a
statement about what must be true, not a report that it is.

## 7. Spend control is visibility, not a cap

Unbounded watch is how the operator says he will often run this, so a design
whose spend story is a flag you must remember is a design that nags in the
common case and fails in the one it was written for.

- **Per-run cost and a session running total go on the channel**, from the same
  recorded run evidence `yoyo cost` already prices (`internal/cli/cost.go`,
  `runstate.Store.Price`). Visibility is continuous and costs nothing to ask for.
- **Any budget is an optional configuration value**, not a session flag.
- This **supersedes the `--budget` session flag shape recorded on ifd.119**,
  which is live today at `internal/cli/schedule.go:48` with `ScheduleSpend`
  behind it. The pre-supervision world is where that shape made sense.

One property of the current implementation is worth carrying over rather than
rediscovering: `--budget` fails closed. A pass that cannot price itself is
refused before anything starts, and a session meeting an unpriceable run stops
rather than counting it as free (`internal/cli/schedule.go:269-273`). A budget
that becomes a config value should keep that rule — a bound nothing can measure
must not be reported as a bound — while a session with no budget configured
prices for display and never stops.

The floor convention should carry over too: `yoyo cost` marks a total with
unpriced runs behind it as `≥ $N` rather than `$N`
(`internal/cli/cost.go:196-204`), and a session total on the channel that
silently omitted unpriced runs would be the one number an operator most needs to
be able to trust.

## 8. What the invariants require of this design

Both supplied invariants bind this architecture, and neither is mentioned by the
work item. They are stated here so each implementation child inherits them from
the section it builds.

**`one-promotion-per-target-branch`.** Supervision changes which process is a
resident's parent. It must not change who takes the promotion lease. No member
of the supervision tree — not `yoyo chat`, not the product manager, not the
scheduling loop, and not the sink — may acquire, release, or otherwise touch a
target branch's promotion lease, or perform a promotion. The promoting **run's
own harness** takes the lease before it promotes and releases it once the
promotion settles, exactly as it does when the loop is started from its own
terminal. This is a real risk in this design rather than a formality: a
supervisor is a tempting place to put serialization, and putting it there would
move the guarantee out of the store and into agent behaviour, which is the thing
the invariant exists to prevent. The scheduling loop starts runs; it does not
integrate them, and supervising it must not make it start doing so.

**`selected-work-passes-intake-and-records-why`.** Every claim of an item the
operator did not name stays gated on reading the product's intake hold and
finding it clear, and every such claim records its selection reason in the run's
durable state — under supervision exactly as before. Two specific consequences
for §5, because the escalation flow is where a bypass would be easy to
introduce:

- Releasing intake in conversation **is not itself a claim and must not start
  anything**. It clears the hold; the scheduling loop's next pull reads the hold
  afresh and records why it chose what it chose. Nothing may be handed a
  pre-authorized item as part of answering the escalation, because such an item
  would be one that ran without passing the hold that was live when it was
  chosen.
- The product manager raising a brake in conversation, and recommending, is not
  a selection. An item the operator names in answering is exempt from the hold —
  naming it is the operator deciding it is the exception — and that exemption
  belongs to the operator's naming, never to the PM's recommending.

The configuration is re-read before every pull (`internal/cli/schedule.go:78`),
which is what makes the first consequence cheap to honour: the loop already
re-reads its world each interval and needs no special resume path.

## What is settled and what is not

Not every section here is in the same state, and treating them alike is what
would let a child be built against a decision nobody made. Three are already
decided — by the operator on 2026-08-20, or by the architect long before this
document — and a child may cite those today. Four wait on the architect. The
column says which, and who would have to answer.

| Section | What a child building it delivers | State |
|---|---|---|
| §1 Interim → target | The supervision built so the launchd migration is a re-parenting | **Shape settled** (operator: headless PM service, thin attach/detach client). Open: whether §1's two carry-over conditions are sufficient — architect Q4 |
| §2 The supervision tree | `yoyo chat` spawning and watching two residents; the sink child's explicit environment and the token-stripped environment for the provider subprocess | **That chat supervises is settled** (follows from §6). Open: the form of the credential boundary — architect Q1 |
| §3 What the chat owns | The two exit policies — sink dies, loop detaches — and the exit path that applies each | **Open** — architect Q2, plus the operator's question on the detached loop |
| §4 Reattachment and the lease seam | The read/submit split beside the unchanged exclusive turn lease; a returning chat's reattach | **Open** — architect Q3 |
| §5 The brake in conversation | The raise: PM notices a brake-placed hold, presents triage and a recommendation, a person answers; ifd.129's path untouched | **Nothing contested**, but unratified: no open question stands against it, and it still wants the architect's hand before a child cites it |
| §6 Residency | 125.3 amended to drop its service tier | **Settled** — the operator decided it; citable today |
| §7 Spend as visibility | Per-run and session-total on the channel; `--budget` retired to an optional config value | **Settled** — the operator decided it; citable today |
| §8 Invariants | Not a child; a constraint every child above inherits | **In force already** — the architect's, and not this document's to settle or loosen |

So the epic is not blocked end to end. §§6 and 7 can be built now against
decisions their owner has already made, and §8 binds whatever is built. What
cannot be built yet is the supervision tree and the residency and lease
behaviour that sit on top of §§1–4, because a child citing one of those would be
citing this document's proposal rather than a decision.

## What the architect is being asked

These four are what [What is settled and what is
not](#what-is-settled-and-what-is-not) marks open, and they are the whole of what
stands between this draft and a design the epic's children can build against. An
answer to each — however brief, and "as drafted" is an answer — closes the
section it names.

**Q1.** **Is §2's explicit-environment rule the right form of the credential
boundary**, or should the sink never be a child of anything that spawns a
provider — supervised by handle but started some other way? The rule as drafted
keeps decision 2's purpose while accepting that the guarantee moves from
"impossible" to "constructed correctly in one place".

**Q2.** **Is §3's asymmetry right** — sink dies, loop detaches — or should the
loop also die with the pane and be restarted on reattach, accepting a park at the
next boundary in exchange for one rule instead of two?

**Q3.** **Is §4's read/submit split the right seam**, or does the assistant
reaching the PM belong behind the same exclusive lease, serialized rather than
split? The split is proposed because the lease's own stated reason is about
resuming provider sessions, and reading is not that.

**Q4.** **Does §1's "re-parenting rather than rewrite" claim hold** under the two
conditions it names, or does the interim step need a third condition to keep the
launchd migration cheap?

## What the operator is being asked

Whether §3's detached scheduling loop is acceptable in the interim — a loop that
survives the pane with nothing supervising it until the PM service exists. It is
no worse than a separately-started `yoyo work --watch` today, and it is the
direct consequence of not wanting to lose in-flight runs when a pane closes; but
it does mean that between chat exit and the service tier landing, there is a
process running that nothing will restart.
