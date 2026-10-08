---
id: portable-agent-configuration
kind: design
title: 'Portable agent configuration: materialization, the baseline, and bundle boundaries'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-01T19:35:00Z
      reason: drafted by the developer under yoyodyne-ifd.36, carried whole to the architect, and ratified with amendments recorded in conversation - the authority paragraph rebound to authority-by-capability and configuration-never-grants-authority with role definitions excluded from materialization, draft scaffolding replaced by the ratification decisions, and conversion output placed under the working-tree publication rule. The four questions the draft asked the architect are answered in its Decided-at-ratification section
    - action: amended
      by: architect
      at: 2026-09-07T00:30:00Z
      reason: 'yoyodyne-ifd.294 - the config.lock baseline ratified as a contract: bundle name, bundle revision digest, per-value digests and never values, a version field, generated and committed, nothing in the load path reading it, held by a conformance test; recorded in the baseline section, the 2026-09-02 ratification entries confirmed present'
    - action: amended
      by: architect
      at: 2026-09-25T04:00:00Z
      reason: approved amendment eb9b385e from yoyodyne-ifd.418 - the same retirement; 'already applies' in place of 'in force'
    - action: amended
      by: architect
      at: 2026-10-07T15:09:27.091741Z
      reason: 'yoyodyne-ifd.434.8: section 6 added. Values the harness recommends (checks, landing checks, developer models and slots, recurring task schedules and models, numeric limits) are decided by the development manager and applied by a typed write refused for anything naming an agent, role, persona, remit, role definition, account, or approval policy; the value reaches the committed file through the reviewed path documents use, bound to the value the role saw, written safely with its baseline in one change, and an uncertain outcome is settled before any retry; drift and doctor say who applied it; race check narrowing is ruled under the operator''s October 3 conditions; the closing question to the operator is decided rather than left open. Resubmitted unchanged after the previous runs judged nothing: one check stopped by its time limit, one push refused by the forge''s own server error.'
approvals:
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-10-07T15:09:27.091741Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 567, for document-567.1 under the automatic approval policy
---
# Portable agent configuration

It serves the goal "Keep roles, policies, and provider selection configurable
without making safety invariants optional."

The work item asked four questions and told the developer not to start by
writing code. This answers the four, states what stays undecided, records the
architect's decisions at ratification, and, in section 6, says how a value the
harness recommends is applied by a role rather than pasted by the operator.

## What already exists, so nothing below re-derives it

The resolution machinery is built and tested. Up to three layers produce the
effective configuration, later ones winning: harness defaults, a bundle named by
`extends`, then the project file. `checks` is replaced as a whole list rather
than concatenated, a `persona` override replaces rather than merges, an agent is
removed with `disabled: true` rather than by omission, and `version` is never
inheritable. The rules are stated in
[Precedence](../configuration.md#precedence) and
[Merge and removal semantics](../configuration.md#merge-and-removal-semantics);
[What fails closed](../configuration.md#what-fails-closed) lists what a
configuration is refused for.

`yoyo config show --origins` records, per key, the layer that supplied it. The
effective configuration has a revision — `cfg-` and a digest derived from the
values rather than declared — which a run record names when it says which
configuration set it up.

Two things are missing rather than broken. There is no way to move a project
from an explicit configuration to an inheriting one; only
[the other direction](../configuration.md#converting-an-inheriting-configuration-to-an-explicit-one)
is documented, and it is a manual re-application. And a project that has
materialized its defaults has no mechanism at all by which a later bundle
improvement reaches it — the standing advice is to re-run `init` into a scratch
directory and diff, which is the gap this design is mostly about.

## The decision underneath all four answers

**Ownership is materialization, and it is not a second vocabulary.**

The tempting design adds a way for a project to declare what it owns — an `owns`
list, a per-key marker, a partial `extends`. Every version of that is a second
way to say something the file already says by having a value in it, and this
repository has already paid for one of those: a proposal in the conversation and
a proposal in `yoyo amendment` are two vocabularies for one idea, and
consolidating them is recorded as not done. So a written value is an owned
value, an absent value is an inherited one, and nothing new is added to the
schema that resolution reads.

What is added is **a record of where a materialized value came from**, kept
beside the configuration rather than inside it. That record is what turns the
two-way diff the operator does today into the three-way comparison the problem
actually needs.

## 1. What a project owns versus what it inherits

A project owns every value it states and inherits every value it does not. That
is already true; what this design fixes is that today it is an all-or-nothing
choice made once, because `yoyo init` writes a complete file with no `extends`
and the only supported way back is to regenerate.

Selective inheritance needs no new merge semantics. A project that writes
`extends` and then states forty of the eighty values it could state already owns
forty and inherits forty, per rules that are built and tested. The three things
missing are a command that produces that file from an explicit one, a command
that produces an explicit one from it, and a way to see which is which. Those
are sections 3 and 2.

**Some values a project cannot inherit however it is shaped, and the set does
not grow quietly.** They divide into three reasons, which are worth keeping
apart because they fail in different places.

- **Refused from a bundle.** `version`, because a version taken from a bundle
  lets a file written against another schema load as whatever the bundle said;
  and `product`, because a bundle that supplied product identity would name
  every project after itself. `checkBundleDocument` refuses a bundle declaring
  either, so this fails when the bundle loads rather than when a project uses
  it.
- **Never supplied by one.** `checks` describe the managed project's toolchain,
  which a bundle has no view of, so `builtin:v1` deliberately states none. A
  project's checks are its own by there being nothing to inherit.
- **Reachable only by opting in.** `approvals.work_items` and
  `approvals.publishing` are stated by the bundle at the same value the harness
  default holds, so extending inherits neither and upgrading moves neither. That
  is existing behaviour and this design keeps it: an opt-in that arrived by
  inheritance would not be one, and it is exactly the class of value a
  `materialize`/`extract` round trip must not quietly relocate.

## 2. Seeing which is which without asking

The work item asks how an operator sees what is inherited without running a
command, given that `config show --origins` answers only when asked.

**The honest answer is that the file cannot carry it.** Anything the harness
writes into `.yoyodyne/config.yaml` to mark provenance is either schema — which
means resolution reads it, which means a project can lie about its own
provenance — or a comment, which the operator edits away or leaves to go stale.
A configuration whose comments disagree with its values is worse than one that
says nothing, because it is read as an answer.

So the design does not put provenance in the file. It makes provenance arrive
**on commands the operator already runs**, which is the same shape this
repository already uses for the ignore-rule warning and for artifact staleness:
reported beside the thing you asked for, never a separate errand.

- `yoyo config validate` and `yoyo doctor` report drift against the recorded
  baseline (section 4) on every run, on standard error, without changing their
  exit codes. A project with no drift says nothing.
- `yoyo config show` gains a fourth line in its header — the bundle a project
  materialized from and whether it is current — beside the layers and the
  revision it already prints.
- The conversation surfaces it where it changes what the operator would decide,
  and nowhere else.

**Every one of those is a projection of one derivation, computed server-side
once.** Drift is a domain derivation, so the CLI, the dashboard, and Slack read
it from the shared read model rather than each computing it; a second surface
computing "is this project current" differently is a disagreement only the
operator can settle, and the invariant on operator surfaces rules it out.

## 3. Moving between explicit and inherited, in both directions

Two commands, each the inverse of the other, and one criterion that makes both
checkable.

**`yoyo config materialize`** turns an inheriting configuration into an explicit
one. It resolves the effective configuration, writes it as a complete standalone
file with no `extends`, copies the personas into `.yoyodyne/personas/`, and
records the baseline of section 4. It writes what already applies, so it
cannot lose a project value. This replaces
[the four manual steps](../configuration.md#converting-an-inheriting-configuration-to-an-explicit-one)
documented today, whose step 3 is "re-apply what was yours" and whose failure
mode is forgetting one.

**`yoyo config extract`** turns an explicit configuration into an inheriting
one. Against a named bundle, it removes every value byte-identical to what that
bundle supplies, writes `extends`, and keeps the rest as deviations. It reports
what it kept and why, in two classes the operator reads differently: values that
differ from the bundle, and values that are not inheritable at all (section 1).
A persona whose body differs from the bundle's is kept as a file and an
override, because a persona is replaced whole rather than merged and half of one
persona is guidance nobody wrote.

**The criterion for both: the effective configuration does not move.**
`yoyo config show --effective` before and after must be byte-identical, and the
configuration revision must be unchanged. That is a property a test asserts
rather than a claim a reviewer reads, and it is what makes the round trip
`materialize` → `extract` → `materialize` safe to offer. Where a conversion
cannot hold it, it refuses and names the value, rather than writing a file that
runs differently from the one it replaced.

Neither command is destructive without saying so: both refuse to overwrite
without `--force`, both fail before writing anything, and both go through the
shared safe-write primitive under the project root, so neither can be walked out
of the repository by a symlink in `.yoyodyne`. Both commands' output is subject
to the same working-tree rule as every other harness write to the primary
checkout — the operator commits it, and runs refuse over the uncommitted
change — so conversions land the way approved artifact writes already do rather
than inventing a third publication shape.

## 4. How a bundle improvement reaches a project that materialized

This is the specific thing an explicit configuration trades away, and the
mechanism that buys it back is **a baseline, recorded at materialization, that
is never read at load time**.

`materialize` and `init` write `.yoyodyne/config.lock`, and its shape is a
ratified contract: the bundle's name, the bundle's revision digest, and the
digest of each value as that bundle supplied it — digests, never the values
themselves; a version field, because a committed file that outlives schemas
needs one, by the same rule `config.yaml` follows; generated, never
hand-authored; committed with the configuration; and **nothing in the load
path reads it**, held by a conformance test asserting the loader never opens
the file. Any implemented format meeting that contract is ratified as it
stands; anything beyond it — values rather than digests, anything a loader
consults — is a deviation to correct. What the file says is still exactly what
runs, which is ifd.35's guarantee and is not weakened here.

With a baseline, `yoyo config drift` is a three-way comparison rather than a
diff, and it sorts every value into four answers:

| Answer | What it means | What is offered |
| --- | --- | --- |
| unchanged | Neither you nor the bundle moved it. | Nothing. |
| yours | You changed it; the bundle did not. | Nothing. It is yours, and it is never touched. |
| available | The bundle improved it; you never edited it. | Adopting it. |
| conflicting | Both moved it, to different values. | Both values, named, for you to decide. |

The middle two are the entire point. Today's advice — regenerate into a scratch
directory and diff — is a two-way diff with no base, so it cannot tell a value
you deliberately changed from a value the bundle improved, and it reports both
as differences. The baseline is what supplies the missing third side.

`yoyo config adopt <key>` takes one available improvement and rewrites that
value and its baseline entry. Adoption is per value and never wholesale: a
command that adopted everything available would be `init --force` with better
manners, and would silently move values the operator had reasons for. A
conflicting value is never adopted; it is reported until it is settled. The
command is designed but not yet built; a role adopts an available value through
the path in section 6, which needs no command a person runs.

**A missing or stale lock is a report, not a refusal.** A project that predates
this, or that deleted the file, gets told once that its baseline is unknown and
that `yoyo config drift` cannot answer for it — the same treatment a document
with a broken relationship gets, and for the same reason: refusing would break a
project over a file that decides nothing about how it runs.

## 5. Where bundles come from, and where this meets the plugin contract

Today one bundle exists, `builtin:v1`, embedded in the executable and looked up
through a fixed map so an unknown name can never resolve to a path. The question
is whether a bundle may ever come from somewhere else — a plugin, a fleet
repository, an organization's house defaults — which is where this meets
yoyodyne-ifd.32.

**The position this design takes: yes, but only as a materialization source,
never as a load-time layer.**

A bundle from outside the executable is read once, by `yoyo config materialize
--from <bundle>`, at the operator's explicit instruction, and its values land in
the project's own file where the operator reads them, edits them, and commits
them. It never becomes a layer that resolution consults on a run. The
consequences are the reason for the rule:

- No third party's content is in the load path of a run. Upgrading a plugin
  cannot move a value the operator is running under, because the value is in
  their file.
- The operator sees what they adopted, as values, before anything runs on them.
  A supply chain that reaches the harness through a diff the operator reads is a
  different risk from one that reaches it at load.
- The baseline of section 4 works unchanged: it records which bundle and which
  revision supplied each value, whoever supplied it, so drift and adoption
  behave the same for a plugin bundle as for the built-in one.

**What a bundle may never do, whoever supplies it.** Authority *semantics* stay
in Go, and composition becomes protected operator-activated configuration only
after parity, per [authority-by-capability](../decisions/authority-by-capability.md)
and the invariant `configuration-never-grants-authority`. A persona specializes
how a role works and cannot widen what it is allowed to do, and the role
contract is sent ahead of it on every turn. A bundle is **ordinary**
configuration, so materialization never writes role definitions:
`materialize --from <bundle>` refuses bundle content addressed to
`.yoyodyne/roles/`, because protected role definitions have their own
operator-activation path and never arrive by materialization from anyone's
bundle. `checkBundleDocument`
already refuses a bundle that declares a `product` or extends another bundle,
and its checks apply to any bundle rather than to the embedded one. A
third-party bundle is content, and every write of it passes the shared safe-write
primitive under a declared root — an unpacked bundle that resolves outside that
root is refused, per
[repository-writes-are-physically-confined](../decisions/invariants/repository-writes-are-physically-confined.md).

That is the whole of what portability means here: **values travel; authority
does not.**

## 6. Values the harness recommends, applied by a role

The harness often knows a better value than the one a project runs under: a
landing check list, the models developers use, the slots that prefer one label,
how often a recurring task runs, a numeric limit. Until now each of those waited
for the operator to paste it into `.yoyodyne/config.yaml`, because that
directory is protected and a run may not write it. A value the harness itself
recommends that only a person can apply is a defect in the harness, so this
section gives each such value a role that decides it and a reviewed path by
which the decision reaches the committed file.

### Which values a role may apply

A value may be applied by a role when it changes how work is done and never who
may do it. These are those values, and the role that decides each:

| Values | Who decides |
| --- | --- |
| `checks` and `landing_checks`, including how a check's scope is narrowed | the development manager |
| `developer_models` and `developer_slots` | the development manager |
| the schedule and model of a recurring task | the development manager |
| numeric limits, such as retries before reconciliation and check time limits | the development manager |

A program manager may propose a value for its own lane, in its lane report or as
a proposal to the development manager, and never applies one. The Lead Product
Manager and the operator may each direct a value, and the development manager
applies it by the same path.

Every other key is refused before anything is prepared. The write is refused
for any value that names an agent, a role, a persona, a remit, a role
definition, an account, or an approval policy, and for `product` and `version`,
because each of those decides who may act or what the project is. The refusal
names the key and says the value stays the operator's. The list of applicable
keys is held in Go beside the role contracts, so configuration can never widen
it.

### The write a role makes

A role applies a value with one typed action that names the key, the new value,
the reason, and a digest of the value the role saw when it decided. Nothing is
written if the key is refused, if the value does not validate as configuration,
or if the effective configuration it would produce fails to load. The action is
recorded, with who made it and why, whether it lands or not.

### How the value reaches the committed file

The value reaches the file the way a document of an automatically approved kind
does, as the [artifact contract](artifact-contract.md) describes, and through no
other path:

- **Prepared apart from the primary checkout.** The harness prepares the change
  in a separate copy of the repository, never in the operator's working tree.
- **Bound to what the role saw.** The change records the expected current value
  and the intended new one. When it is prepared, and again when it lands, the
  harness compares the file's current value with the expected one; if they
  differ, the change is refused and returned to the deciding role with both
  values, never merged over a newer edit.
- **Written safely.** The file and its lock are written through the shared
  safe-write primitive: each is written in full to a new file that refuses an
  occupied name, flushed, and renamed into place, with the directory flushed
  after, so no reader ever sees half of either.
- **The value and its baseline move together.** The value and its entry in
  `.yoyodyne/config.lock` are one change, published together or not at all, so
  drift never sees one without the other.
- **Reviewed like any change.** The harness opens a run that carries exactly that
  change and may change only those two files, with no developer rewriting it.
  The run is checked and independently reviewed and lands through the normal
  integration path. A run that does not land goes back to the deciding role with
  the findings, the failing check, or the conflicting paths.
- **An uncertain outcome is settled before any retry.** If the harness cannot
  tell whether a write or a landing happened, it reads the file as it now stands
  and compares it with the intended value before doing anything else. It
  retries only when the value is absent, and records success when the value is
  already there.

### What drift and doctor say about a value a role applied

A value applied by a role is the project's own, so drift reports it as yours,
and adds who applied it, when, and the change that carried it. `yoyo doctor`
lists the values roles applied in the last week with the same three facts. An
available improvement from the bundle is put to the development manager to
decide; it is not put to the operator.

### Narrowing the race check

A check's scope, such as the packages the race check covers through
`RACE_PACKAGES`, is a value the development manager may narrow by this path,
under these conditions and no others:

- Ordinary tests, formatting, vet, and the race check required on CI keep their
  full scope.
- A full check of the actual merge candidate runs before any narrowed check
  counts, and the coverage this repository needs on macOS is kept; tests on
  Linux and builds for macOS made on Linux do not stand in for running on macOS.
- A scope that is unset, unreadable, or ambiguous runs the full check.
- A scope set explicitly to nothing runs no race check, never tests the module
  root by accident, and is never reported as full coverage.
- The run record keeps the scope that was intended and the scope that ran apart,
  each bound to the configuration revision, so a narrowed check is never read as
  a full one.

The implementation needs a grant limited to the one scope key and its lock
entry, and the reviewed path above; it does not wait for the rest of this
section to be built.

## What this design deliberately does not decide

- **A bundle registry, a resolver, or a fetch protocol.** Section 5 decides
  where a third-party bundle may be used and what it may not do. How one is
  named, discovered, verified, or pinned belongs to the plugin contract, and
  deciding it here would be this document legislating for a design it is not.
- **Whether `yoyo init` should change what it writes.** It should not, on this
  design's evidence — ifd.35's trade holds, and the lock is additive.
- **Fleet configuration across several projects.** One repository at a time.
  Several projects sharing defaults is what a bundle is for; a fleet that also
  wants shared *state* is team mode's problem, not this one.
- **A migration for projects that predate the lock.** They report an unknown
  baseline and keep working. Whether `materialize` should offer to reconstruct
  a baseline by matching current values against a bundle is a real question with
  a real wrong answer — reconstructing one is guessing that unedited values were
  never edited — and it is left open.

## Decided at ratification

The architect ratified this design with four decisions, recorded in
conversation (chat-11558d325e9a214ebfd00bb4a0012750, turn 24):

1. **"Ownership is materialization" is the right refusal.** An `owns` list is a
   second vocabulary for what the file already says by having a value in it,
   and this repository has paid for exactly one such duplication already. A
   written value is owned; an absent value is inherited; nothing new enters the
   schema resolution reads. Ratified as the design's spine.
2. **The committed lockfile is the right shape.** A baseline in the state
   directory is per-machine, tells a collaborator nothing, and drifts per
   checkout. The property that makes it safe is kept verbatim: **nothing in the
   load path reads it.** What the file says is what runs; the lock only makes
   the three-way comparison possible. Committed, generated, decides nothing.
3. **"Materialization source, never a load-time layer" is the right boundary,
   and the plugin contract inherits it rather than re-deciding it.**
   Third-party content reaching the harness through a diff the operator reads
   is a categorically different risk from content consulted at load, and no
   plugin design gets to reopen that; yoyodyne-ifd.32's contract cites this
   design instead of arguing with it.
4. **Drift is a read-model derivation from the start.** "Is this project
   current" is a domain derivation that at least three surfaces will show;
   `surfaces-project-one-read-model` rules out a CLI-local computation.

## How much the drift report says

This section replaces a question earlier revisions left with the operator, which
held back the conversational surfacing of drift. It is decided here, because
how loudly a report speaks is a design choice and not a change to what the goals
admit. The drift report says nothing when nothing changed, and speaks only on
commands already being run and in the development manager's conversation. An
available improvement goes to the development manager to decide under section 6;
it is never put to the operator as a question. A conflicting value is reported
to the development manager until settled. Nothing about drift waits on an
operator's answer.
