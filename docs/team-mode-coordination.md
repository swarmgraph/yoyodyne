# Distributed coordination for team mode

Drafted for yoyodyne-ifd.82.2, the architecture pass of the team-mode epic.
**Status: draft — the architect's ratification and the operator's decision on the
questions at the end are outstanding.**

It lives outside the artifact homes for the reason
[team mode scope](team-mode-scope.md) and [the documentation map](docs-map.md)
do, plus one of its own. (The map is a structure decision for two documentation
splits rather than an index of `docs/`, so it enumerates neither that document nor
this one, and neither needs a row in it.) The shared reason is that this run may
not write into `docs/designs/`; the reason of its own is that it should not want
to.
Designs are the architect's, creating one refuses every other role, and a design a
developer filed under the architect's name would be exactly the silent
redefinition of upstream intent the protected homes exist to stop. So this is a
draft to be adopted: when the architect ratifies it, it moves under
`docs/designs/` with identity frontmatter, the architect's own revision entry, and
`supports: v1-goals` — subject to the note below on that goal's standing.

It designs against [team mode scope](team-mode-scope.md), which states on its own
first line that the Lead Product Manager drafted it and the operator approved it in
conversation on 2026-08-19, and it serves the v1 goal *"A team can run Yoyodyne
against one shared repository: collaborators each run their own harness without
losing work, splitting the tracker, or weakening any safety invariant."*

**On that goal's standing.** The work item commissioning this design carries a
caveat: the team goal awaited the operator's pen, was accepted while goal
validation was down, and should be verified when yoyodyne-ifd.1.9 seeds the
store. The record answers the first half of that. `docs/product/goals/v1-goals.md`
states the goal; its revision log records the Lead Product Manager adding it on
2026-08-19, in the amendment that also amended the non-goals; and its approvals
carry an operator entry against exactly that revision, reading *"Approved by the
operator in conversation on 2026-08-18, 'Both Approved': the team goal, drafted by
the Lead Product Manager for the team-mode epic (yoyodyne-ifd.82)."* So the goal is
recorded and approved rather than provisional, and the sentence above cites that
record rather than asserting it.

The second half is open, and the `supports` line above is conditional on it. This
document's drafting run could not confirm that the harness *resolves* the goal —
`yoyo goals list` and `yoyo artifact show v1-goals` were both unreachable in its
environment — so it rests on what the file states. The architect should run those
two commands when adopting, rather than take this paragraph's word for it.

It covers the five recorded gaps — tracker sync, distributed claim, a promotion
lease that survives leaving one machine, state that has to travel, and operator
identity — and the two scenarios the operator recorded against the epic: the
shared Slack channel with two sinks in it, and per-item budgets that are per
machine.

## The one idea underneath all of it

Every durable thing the harness holds is a fact about one of three things, and
which one decides where it lives. The test is not what the state is called; it is
what two machines disagreeing about it would mean.

**Product facts** are true for everyone or true for nobody. The backlog, who
claimed what, what an item is for, what agents reported, what triage has already
spent on an item, what an item has cost. Two machines holding different answers
is a *disagreement*, and a disagreement about a product fact is the tracker being
split. These travel.

**Machine facts** are facts about one collaborator's harness. Whether their
provider spending is paused, which runs their processes hold, their worktrees,
their event logs, their conversations. Two machines holding different answers is
two *correct* answers — my machine is paused and yours is not, and neither of us
is wrong. These stay local, and making them travel would be taking control of
somebody else's laptop.

**Repository facts** are facts about the code: where a branch points, what has
been merged. These are already shared, by the forge, and this design adds nothing
to them.

The scoping document already applies this split once, to the Slack scenario:
directives are product intent and bind whichever machine received them, while
pause, stop, and intake hold are each collaborator's own. What is above is that
line generalized, so the next piece of state does not need a separate ruling.

### Where the product facts live

They live in **one shared coordination store, carried on the same Git remote the
repository already uses**, beside the tracker rather than inside it: the tracker
under `refs/dolt/data` as Beads already moves it, and the harness's own shared
records under a ref of its own. One remote, one permission model, nothing to
stand up, and the amended no-hosted-control-plane non-goal is honored.

Two records-store shapes were considered and one loses.

*Everything into Beads*, as work-item metadata and notes, is what the operator's
recorded sketch reaches for, and for the tracker's own data it is simply right —
Beads is a full Dolt replica with cell-level merge and it already syncs. It
loses for the harness's *runtime* records. A report about the environment, a
directive that names no item, and a promotion turn are not work, and putting
them in the backlog makes them work: they appear in `bd ready`, they need types
and labels that mean nothing to anyone reading the backlog, and yoyo's durable
state becomes a hostage to another tool's schema. The harness shells out to `bd`;
it does not own that database's shape.

*A store of yoyo's own on the same remote* keeps the operator's architecture —
same forge, same authority, no server — and keeps yoyo's records yoyo's. Each
record is a file named by its own identifier, so two machines writing at once
write two different files and the merge is a union with nothing to resolve. That
is the shape almost all of this state already has: `ReportStore` is an
append-only log, `DirectiveStore` is one file per directive created exclusively,
triage counters are increments against a cap. They are append-only because a
report is written once and a directive is revised once, and append-only is
exactly what union-merges without a conflict.

So: **the tracker carries the backlog and the claims; a shared record store on the
same remote carries the harness's product facts.** Both sync at the same moments,
in one operation, so an operator has one thing to think about and not two.

**Where these records are written is constrained, and the constraint is named here
rather than left for an implementer to rediscover.** This store is a new family of
writers — a file per claim, report, directive, budget increment, and promotion
turn — and
[`repository-writes-are-physically-confined`](decisions/invariants/repository-writes-are-physically-confined.md)
holds over every one of them. Two cases, and which one applies is decided by where
the bytes land rather than by what the record is called:

- A record written into any repository the harness manages — a checkout it stages
  the shared ref from, or the Dolt database directory beside the tracker — is a
  repository-scoped write. It goes through `internal/repowrite` declaring that
  repository as its root, like every artifact and invariant write already does. No
  new low-level writer is introduced and no caller opts out.
- A record staged in the machine's own state root, beside runs and reports, is
  runtime state rather than a repository write, and is written the way the run
  state store already writes.

The boundary between the two must be explicit in the implementation rather than
incidental. The invariant's stated risk is precisely a new writer silently
lacking containment somebody else's writer has, and a store whose records move
between a state root and a repository is where that would happen unnoticed.

### What this costs, stated up front

Between syncs every machine is working on a replica, and a replica is a reading
of the past. That is the offline story for free and the race window for free with
it. Nothing here removes that window; what the rest of this document does is make
the window short, make losing it cheap, and make it unable to corrupt code.

## Wiring: making the remote exist

The operator's recorded sketch, from 2026-08-18, says nothing configures the Dolt
remote and every `bd` call warns about it. **That is no longer true**, and the
correction matters because it decides how much of this gap is left to build.

`yoyo init` configures the tracker's sync remote today. It reads the Git remote
`origin`, points the tracker's remote of the same name at it, reads back what was
actually stored, and reports one of four outcomes — configured, unchanged,
skipped, or failed. A tracker already holding an `origin` is left exactly as it
is, even when it points elsewhere; `--tracker-remote` names a URL instead, for a
tracker kept in a repository of its own; and a project with no Git remote, or
whose `bd` is not initialized, is told what to run rather than failed.

So the tracker half of the wiring is **built, not pending**. What remains is the
shared record store's half: the same one-time setup, off the same remote, in the
same command, so a machine acquires both or neither and there is no state in
which the backlog syncs and the harness's own records do not. A collaborator
joining an existing project runs that one command; with the readme and repository
access, it is the whole of what a second machine needs.

A project with no Git remote gets no shared store and is told so rather than
failed, exactly as `init` already treats the tracker. A harness with no shared
store is a single-machine harness, which is what every project is today.

**Team mode requires publishing.** A harness whose `approvals.publishing` is off
promotes by moving a branch on its own machine and pushing nothing, so two such
harnesses share a repository in name only and the forge is not the integration
authority the scoping document promises. A configuration that declares a shared
coordination store and leaves publishing off is refused when it loads, naming
both settings. This is not a new restriction so much as the scoping document's
own promise made checkable, and the promotion design below rests on it.

## The tracker: sync at the moments that already exist

The sync points are not invented. The run pipeline already consults the
directive store at three boundaries — before it starts a run, before it resumes
one, and before it puts a change through the gate — and the harness already
reads the intake hold before it selects work nobody named. A consultation of
shared state is worthless if it reads a stale replica, so **every existing
consultation point becomes a sync point**, and no new cadence is invented:

- **Pull before claiming**, and before any decision that reads shared state to
  decide whether to spend: selection, the intake-hold check, a triage decision
  against a cap, the three directive consultations.
- **Push after every shared write**: a claim, a status change, a note, a report,
  a resolved directive, a spent budget.
- **Pull and push at session close**, which is the only sync that exists for its
  own sake.

Each is a Git fetch or push against a remote the machine is already
authenticated to. A pull per claim is cheap; a pull per provider call would not
be, and none is proposed.

A **push that fails** leaves local writes unpushed. Nothing is lost — the replica
holds them and the next sync carries them — but a write nobody else can see must
not be acted on as though they could. So the claim protocol below fails closed on
a push it could not complete, and every other write is retried at the next sync
point.

A **pull that fails** leaves the replica stale. The harness may keep working on
what it holds; what it may not do is choose new work on a reading of the hold it
knows is old. See the question to the architect at the end — this is the one
place where how stale is too stale is a ruling rather than a derivation.

### The `.beads/issues.jsonl` export

Stop committing it where a sync remote is configured. It is a passive export of
the Dolt database, it carries no information the database does not, and under two
writers it is a file that conflicts on every merge while telling nobody anything.
The alternative — one machine owns the export — works and costs a rule nobody can
see being violated. Regenerating it on demand is what a reader actually wants.

## Claiming work without two machines paying for it

A claim is a write to the tracker: the assignee, the time, and the machine. Two
machines claiming one item inside the sync window write the same cells, Dolt
detects it as a same-cell conflict on merge, and the resolution is deterministic
rather than negotiated.

**The protocol**, which is the sketch plus one step:

1. Pull.
2. Read the intake hold from the pulled replica; a hold that is active stops
   selection here, and the reason the item was chosen is recorded with the run,
   as the intake invariant requires.
3. Claim the item locally.
4. Push. A push that does not complete abandons the claim: the item is released
   locally and the harness picks again or stops. A claim only this machine can
   see is not a claim.
5. **Pull again and confirm the claim still resolves to this machine.**
6. Only now start the run.

Step 5 is the added one and it is what makes the cost bounded rather than
nominal. Without it, a losing machine discovers the loss whenever it next syncs,
which may be after a developer has run — the "worst case one duplicated run's
bounded visible cost" the sketch accepts. With it, the loss is discovered inside
one push-and-pull round trip, before any provider is called. The read-back is
also the discipline this codebase already holds everywhere it writes something
another process depends on: a tracker remote, a work-item update, and a blocker
are each read back rather than assumed.

**Resolving the conflict.** The earliest claim wins, and the loser releases and
picks again. Two things make that safe to state:

- It must be **deterministic**, not fair. Both machines resolve from the same
  merged rows, so both must reach the same answer without talking. Equal
  timestamps and clock skew both break "earliest" as a total order, so the order
  is the pair *(claim time, machine identifier)* — the time first because that is
  what an operator would expect and what the sketch endorsed, the machine
  identifier because it is a total order that always breaks the tie.
- Skew is bounded by nothing the harness controls, so a badly-skewed machine wins
  claims it should have lost. That is a nuisance and not a hazard: the loser
  releases, picks again, and nothing about the code is affected. It is worth a
  report rather than a mechanism — a machine whose claims are consistently
  earlier than everyone's is a clock to look at.

**Forge branch protection is the backstop that makes this safe to do
optimistically.** Even a race nobody detects produces two runs of one item, two
branches, two reviews, and two pull requests. It does not produce an unreviewed
merge, because no agent pushes or merges on any machine.

## Promotion across machines

This is the gap that touches an invariant, so it is worth being exact about what
holds today and what does not.

`one-promotion-per-target-branch` requires that at most one promotion per target
branch happens at a time, enforced by a lease in the runstate store. The lease is
an advisory file lock, which the operating system drops when its holder dies.
That mechanism is machine-scoped: it serializes every process sharing one state
root and it says nothing at all about a second laptop.

What is *not* lost across machines is correctness, and this is the part worth
leading with. A promotion is already a compare-and-swap against the shared truth:
the local target is fast-forwarded from the commit the run was written against,
and before the forge merges, the remote target must contain that commit and carry
exactly its content. A target another machine moved fails that precondition, the
promotion **fails closed**, nothing is force-merged and nothing is reset, and the
run replays onto wherever the target went, runs the checks again, and obtains a
fresh independent review. So two machines promoting into one branch is already
safe today, on a repository nobody has coordinated.

What is lost is the *queue*. The lease turns contention into a queue in which the
loser waits and then promotes; without it the loser burns a replay and a second
full review. That is a real cost — a review is the expensive thing the harness
buys — but it is a cost and not a hazard.

**So the design is two levels, and it names which level guarantees what.**

- The **local flock stays**, unchanged, as the queue between processes sharing a
  state root. It is cheap, it drops on crash, and it is right for the case it
  covers.
- A **shared turn record** in the coordination store is the queue between
  machines: write a turn, push, pull, and promote only when this machine's turn
  is the earliest unfinished one for that branch; mark it finished and push. It
  is ordered by the same *(time, machine)* pair claims are, for the same reason.
  It is written, advanced, and finished by **the promoting run's own harness, in
  that process, and by nothing else** — no agent writes a turn, clears one, or
  reads one to decide anything, exactly as no agent touches the local lease and
  no agent performs a promotion. That is the invariant's second half, and it binds
  the cross-machine artifact as much as the file lock it sits beside: an
  implementer must not site the turn record anywhere an agent's worktree can reach
  it, because the roles that authorize a promotion must not be able to take a turn
  at one.
- The **forge precondition is the guarantee.** The turn record is advisory and
  has the same sync-window race everything else here has. It is worth having
  because it converts almost every contention into a wait instead of a wasted
  review; it is not what makes the race unable to corrupt code, and it must never
  be described as though it were.

A turn record whose holder died is the one case a file lock handled for free and
this does not: nothing drops a record in a repository when a laptop closes. So a
turn is bounded by the same wait the local lease already bounds itself by, and a
turn older than that bound is disregarded by everyone. The run whose turn expired
is not harmed — it meets the forge precondition or it replays, exactly as it
would have.

**This narrows the invariant as written**, and the amendment belongs to the
architect. It is raised in this run's summary rather than applied here.

## What else has to travel, and what must not

### Travels — product facts

- **Reports.** What an agent noticed outlives the run, and in a team it outlives
  the machine. A report reaching only the laptop whose run produced it is the
  channel-nobody-reads failure with extra steps.
- **Directives.** Product intent, and the goal is verbatim that they are
  enforceable regardless of which agent received them. In a team that has to read
  "regardless of which *machine* received them", which is the Slack scenario
  below and the reason directives move out of per-machine state.
- **Triage budgets and review rounds.** The scoping document records the gap: an
  item that has spent its grants on one machine has spent none of them on the
  other, and a cap of one is a cap of two across a pair. These bound an item
  across every run of it — the triage store says so in its own words — which is
  the definition of a product fact. One consequence is concrete: review rounds
  are counted today by scanning local run records, so making the count a product
  fact means each run *records* its rounds to the shared store rather than the
  total being derived from files only one machine has.
- **What an item cost.** The same argument, serving the same goal the cost
  reporting already serves: an operator seeing what the harness spent on their
  behalf, in a team, is seeing what it spent on every machine.
- **The intake hold.** See below; it is the answer to team-wide pause.

### Stays — machine facts

- **The operator hold (`yoyo pause`).** It is a fact about an account, a bill, and
  an afternoon away from the machine, and it lives at the state root rather than
  under a product for exactly that reason. Making it travel would let one
  collaborator stop another's work.
- **Stop, and the run leases.** Already the architect's decision for `/stop`, and
  unchanged.
- **Run state, worktrees, event logs, provider sessions.** A run belongs to the
  machine that holds its lease. What another collaborator needs is not the run's
  state but what became of the work, and that is the item's status, its notes,
  its blockers, its reports, and its cost — all of which travel.
- **Conversations.** The scoping document is explicit that this epic moves
  coordination state and not conversation, and there is no team chat surface. The
  resolution is that a conversation's *transcript* is a machine fact and its
  *outcomes* are product facts: an admitted work item, a recorded directive, an
  approval, a decided amendment each already land somewhere that travels. The
  cost is real and worth stating plainly — a collaborator cannot read the other's
  chat history, and a decision argued in a conversation and never written down
  reaches nobody. That is the same cost a single operator already pays across two
  terminals.

### Team-wide pause, answered

The scoping document leaves team-wide pause as a design question and promises
only that the pause switch pauses the machine it is on. The answer falls out of
the classification: *"stop spending my provider budget"* is a machine fact and
does not travel; *"nobody should pull new work from this product"* is a product
fact and does. They are already two different switches in the harness — the
operator hold and the intake hold — and only the second one is about the
backlog.

So the **intake hold moves into the shared store** and the operator hold does
not. A collaborator holding intake stops every harness choosing new work from
that product and stops none of them finishing what they are running, which is
what the narrow switch already means on one machine.

This strengthens `selected-work-passes-intake-and-records-why` rather than
weakening it: the same hold is read before the same claim, by every harness
instead of one. The invariant's exemption is unchanged — an item the operator
named is the operator deciding it is the exception — and it is what keeps a
machine that cannot reach the shared store able to do the work its own operator
asks for.

## Operator identity, designed once

The explicit constraint on this work item is that operator identity is designed
once: jointly for the recorded approval-forgeability precondition and for this
epic's multi-human case. It is designed once because it is *one* question — who
is a human, and what may that human do — and the harness already answers most of
it.

`operators` binds each human's identifier namespaces (git address, forge account,
Slack member id) and grants them authority (`own-intent`, `direct-work`).
Authority attaches to the human rather than to a surface, so an act is authorized
by resolving whichever namespace it arrived through to a person and then asking
what that person may do. At most one human holds `own-intent`, because intent
that can conflict with itself is conflict machinery nobody has designed. None of
that is new here and none of it is changed.

**Three layers, and it matters which is which.**

*Assertion* is Git and Dolt authorship: the address on a commit or a tracker
write is what the author says about themselves. *Proof* is the forge's push
authentication, at the one boundary that is shared. *Authority* is the grant,
resolved from either.

**What this closes for the approval gap.** An approval records `by: operator`
today, on the strength of who ran the command, with nothing distinguishing the
operator from anything else with a shell. With the mapping in place it should
record the resolved *human* — the operator name, the namespace the act arrived
through, and the identifier — and refuse to record an approval by anyone who does
not hold `own-intent`. That turns an approval from an unattributed assertion into
one naming somebody the project recognizes, which is what the mapping was for.

**What it does not close, deliberately.** Git authorship is an assertion and this
design does not make it a proof. It does not need to, for two reasons that hold
in a team as well as alone. Agents cannot reach the goals at all: a conversation
cannot write a file (no tools on Claude Code, reads only on Codex), and a run's change is compared against the protected homes
before any check runs and before any reviewer sees it, so an approval a developer
wrote never reaches the repository the goals are read from. And forging an
approval therefore requires a human with repository access — a trusted peer by
the scoping document's own non-promise that protecting teammates from each other
is out of scope. If either enforcement is ever loosened, this is what was resting
on them, which is what `internal/chat/admission.go` already says and what this
design keeps true.

**Machine identity is a separate thing and is needed here.** Claims, turn
records, and budget writes are ordered and attributed by machine, and a person
may run two. So a machine identifier is generated once at setup, stored in that
machine's state root, and recorded beside the operator on every shared write. It
is not identity and proves nothing; it is a tie-breaker and a label on a record,
and conflating it with the human would make a second laptop a second person.

## The Slack scenario: two sinks in one channel

Two collaborators, shared configuration, one channel, therefore one inbound
allow-list. Can one direct work on the other's machine by replying in a thread
the other's sink owns? The design answers by kind, and by fixing what is
currently an accident.

**Authority is already right.** A reply acts only when its Slack member id
belongs to a human granted `direct-work`, and that list is *derived* from the
operator mapping rather than authored beside it. Thread ownership grants nothing.

**The destination is what changes.** A sink records a reply into its machine's
directive store, so today which harness enforces a directive is an accident of
which sink happened to open the thread. Directives move into the shared store,
and every harness reads it. A reply then binds the work regardless of which
machine received it — which is the directives goal in its own words, now that
"which agent" has to mean "which machine" too.

**Machine controls stay unreachable from Slack**, unchanged. Pause, stop, and a
machine's own intake are not directives, they are not recordable as one, and a
reply cannot reach them. The shared intake hold is the one thing in that
neighborhood that travels, and it is a product fact rather than a machine
control.

**Two sinks must not act twice on one reply — and this is a race like the others,
not an exception to them.** Today a sink remembers what it has acted on in process
memory. That is right for Slack redelivering to one process and it does nothing
across two, so the cross-machine case needs the same treatment claims get, and for
the same reason: inside the sync window neither sink can see the other.

The directive store's exclusive create does not supply this. It is a guarantee
within one machine's filesystem — two sinks each pull, each find no directive for
that message, each create one, and each push, so the exclusive create succeeds on
both replicas and neither ever "finds it recorded". Keying the record on the Slack
message id would be worse than useless: both machines would write the same record
path with different contents, because every shared write carries the machine
identifier, and a same-path conflict is exactly what the union-merged shape of
this store exists to avoid.

So the resolution is the one the rest of the document uses:

- Each sink writes a record with an **identifier of its own**, carrying the Slack
  message id as a field. Two sinks produce two distinct records that union-merge
  cleanly, with nothing to resolve at the filesystem level.
- After pushing, the sink **pulls back** and looks for every record bearing that
  message id. The *(time, machine)* order picks one: the earliest record is the
  directive that binds, and any later record for the same message is superseded
  and binds nothing. Both machines compute that from the same merged records and
  reach the same answer without talking, which is the property that matters.
- The sink **acknowledges in the thread after the confirming pull rather than
  before it**, naming the directive that binds. That is step 5 of the claim
  protocol applied to a different write, and it is what keeps the operator from
  being told two different things about one reply.

**The residual cost, stated rather than argued away.** Two sinks whose
confirming pulls race can each acknowledge before either sees the other, so an
operator can see two acknowledgements for one reply. What they cannot see is two
active directives: the superseded record binds nothing, and the run pipeline
reads the one that won. A duplicated acknowledgement is a cosmetic cost inside
the same sync window every other race here has; it is not exactly-once delivery,
and this design does not claim it.

## What no longer holds, and what is being asked

### The invariant this narrows

`one-promotion-per-target-branch` states its mechanism as an advisory file lock
the operating system drops when its holder dies. That is machine-scoped, so on a
shared repository the invariant as written binds one machine at a time rather
than the target branch. The promotion section above proposes what should replace
it; the amendment is the architect's to make and is raised in this run's summary
rather than written here.

### What the architect is being asked

1. **Is the three-way classification the right spine** — product facts travel,
   machine facts do not, repository facts are the forge's — and is
   *"two machines disagreeing is a disagreement rather than two correct answers"*
   the right test to apply to the next piece of state nobody has classified?
2. **The shared store's shape**: yoyo's own union-merged records on a ref of its
   own beside the tracker, or everything into Beads metadata as the operator's
   sketch reaches for? The former is proposed, and the cost is two stores synced
   in one operation rather than one store.
3. **How stale a replica may be before autonomous selection stops.** The intake
   invariant requires a harness to read the hold and find it clear before
   claiming work nobody named. A replica is readable but old. Whether a hold read
   from a replica the machine could not refresh counts as "found clear", and for
   how long, is a ruling about the invariant's meaning rather than something this
   design can derive. Failing closed protects the operator's control and costs the
   offline story; failing open costs the opposite.
4. **Is the promotion turn record worth its weight**, given that the forge
   precondition is what actually guarantees safety and the record only converts a
   wasted review into a wait? Doing nothing is a defensible alternative and this
   document does not pretend otherwise.

### What the operator is being asked

Whether **team mode requiring publishing** is the right trade. It is what makes
the forge the integration authority the scoping document promises, and it means a
team cannot run with publishing off — which is the configuration a single
operator gets by default today.

### What this design does not do

The scoping document's non-promises are unchanged and nothing here reaches past
them: no hosted control plane, no permissions between users, no team chat
surface, one product and one repository per harness instance. In particular,
nothing stops one collaborator releasing another's claim or resolving another's
directive. They are trusted peers; protecting work from races is in scope and
protecting teammates from each other is not.
