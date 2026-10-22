---
id: ownership-and-the-operator
kind: design
title: Who owns what, and when it reaches the operator
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-09-30T02:00:00Z
      reason: 'yoyodyne-ifd.430 - Mason''s direction of 2026-09-29: one ownership registry and one resolver every surface reads, a closed list of what is the operator''s held by a test, an escalation ladder between roles, and missing capabilities recorded as defects rather than asks of the operator'
    - action: amended
      by: architect
      at: 2026-10-21T02:49:08.793992Z
      reason: yoyodyne-ifd.437.14 - the owning role decides a proposed change to its own documents with the amendment action and reports afterwards; the operator decides only a goals or brief change that alters intent or does not say, and may override. Also folds in approved amendment-93e36b92 (the closed list under the names the code uses, and the escalated brake hold going to the Lead Product Manager with the operator told) and approved amendment-7f69c6ac (owners for the brake hold, a stalled queue, a provider's usage limit, and the harness's pause for capacity). The table keeps the entry kind 'usage window' as main writes it, because the terms register still names it for this document.
    - action: amended
      by: architect
      at: 2026-10-22T07:15:18.422149Z
      reason: 'Approved amendment-5fe0aad5: the table gains a row for a document waiting on approval, which the registry now has a rule for. A brief, goals, non-goals or operating rules document is the operator''s for fundamental intent; any other stays its owning role''s and carries hand while its approval policy needs him. The kind joins the list of kinds. Nothing else changes; ''usage window'' stays as main writes it because the terms register still names it for this document.'
    - action: amended
      by: architect
      at: 2026-10-22T14:53:28.714302Z
      reason: 'Replacing the coined vocabulary in the governed documents, first document (part of yoyodyne-ifd.437.21): the table''s entry kind for a provider''s usage limit is now written in the register''s ordinary words, since the terms-check fix (yoyodyne-ifd.437.48) lets a document drop a retired term its register row names. Nothing else changes.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-21T02:49:08.793992Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1379, for document-1379.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-22T07:15:18.422149Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1473, for document-1473.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-10-22T14:53:28.714302Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1494, for document-1494.1 under the automatic approval policy
---
# Who owns what, and when it reaches the operator

Serves the autonomy goal: the operator states intent and approves the brief and
goals, and the roles run the rest. The operator's rule of 2026-09-26 says what
that leaves him: a change to the fundamental goals, meaning one after which the
goals would admit work they refused or refuse work they admitted. Everything
else consistent with the goals is delegated to the Lead Product Manager, or to
a program manager in its lane. This design adds the acts only a person can
physically perform, because no delegation reaches those.

## The problem this ends

Every surface has worked out on its own who moves an entry, and a case its
rules did not cover fell to the operator. The read model's owed step was
hard-coded to the operator under a comment saying it was the harness's. A
program manager's critical report reached him as something only a person could
act on, when the remedy was the development manager's re-run. Escalations of
empty runs on parked items reached him. Each was fixed where it was found, and
new code kept adding places that decide. The fix is to have exactly one place
that decides, and to make the operator an answer that place gives only for a
named reason.

## One registry, one resolver

A new package, `internal/ownership`, holds the registry. It is a table keyed by
entry kind. The kinds are the ones the read model already names: amendment,
waiting document, directive, report, owed step, publication, degraded service,
failing task, hold, outage, stall, held work, human gate, conversation-carried
item, operator action, stoppage, escalation, diverged target, and the ages of
the report pile and the amendment queue. Each kind has one rule. The rule reads
the entry's record and returns:

- `owner`, from the existing mover vocabulary: the operator, a role, the
  harness, or nobody where the entry lifts on a clock no one controls;
- `reason`, required when the owner is the operator and empty otherwise;
- `remedy`, the one act that ends the entry, in the words the surfaces print;
- `capability`, the registered action or tool the remedy needs;
- `hand`, present only when the owner is a role but the harness cannot yet
  perform the remedy for it (below).

The read model calls the resolver once per entry as it builds the standing.
The dashboard, the Slack sink, `yoyo status`, the conversations' context, the
needs-a-human line, the development manager's pass, and report handling all
project the answer the read model carries. None of them derives an owner. This
is the one-read-model invariant applied to who owns what.

**The default is the Lead Product Manager, never the operator.** A record the
rule for its kind cannot classify resolves to her, with the remedy "classify
this entry": it is an unowned-entry defect, shown on the harness's line and in
her pass, and she files it against the registry.

## The rule for each kind

| Kind | Owner |
| --- | --- |
| amendment | the role that owns the target document, which decides it as described under *Who decides a proposed change*; the operator only for an amendment to the brief or the goals recorded as `intent: fundamental` or recorded with no intent, reason `fundamental-intent` |
| waiting document | for a brief, goals, non-goals or operating rules document waiting on approval, the operator, reason `fundamental-intent`, because a document filed under `docs/product` waits on him only when it changes what the goals admit or refuse, or does not say whether it does; for any other document waiting on approval, the role that owns it, with the operator's approval as the act that ends it, carried as `hand` while its kind's approval policy needs him (yoyodyne-ifd.433.21), and never with the operator as its owner |
| directive | the Lead Product Manager, who ends it or carries it into a document or item; where settling it would change what the goals admit, what reaches the operator is her drafted change to the goals, as an amendment, not the directive |
| report | the Lead Product Manager, who handles it; a critical whose remedy is a role's names that role; the operator only when her handling names a reason from the closed list |
| owed step | the harness |
| publication | the harness while it can replay, re-arm, or wait on a red target; the development manager for a merge handed back; the operator only where the forge refuses the harness's token, reason `repository-setting` |
| degraded service, failing task | the harness or the supervisor; the task's role where the task failed on its own work; the operator only where the remedy is a credential, reason `credential` |
| hold | the operator for a hold or pause he placed, reason `own-hold`; the development manager for the brake's hold on new work, and the Lead Product Manager once the development manager escalates it, with the operator told; the harness for its own pause on new work for lack of provider capacity, which it lifts at the provider's reset or after a successful check of the provider |
| outage, provider's usage limit | nobody, since a provider outage or a provider's usage limit lifts on the provider's clock; the operator only for an expired login, reason `credential` |
| stall | the harness for a stalled queue, shown as a degraded service once the harness has left it down; for a run whose AI session went silent, the harness the first time and the development manager after that |
| stoppage | the development manager |
| escalation | the role it names; the operator only when it carries a reason from the closed list |
| held work | the owner of the group it is held in |
| human gate | the operator, reason `human-gate` |
| conversation-carried item | the role its executor marker names |
| diverged target | the operator, reason `diverged-history` |
| pile or queue age | the role that works that pile or queue |

## Who decides a proposed change

*(yoyodyne-ifd.437.14)*

A role that does not own a document proposes a change to it rather than
making one. The proposal waits on the role that owns the document, and that
role decides it:

- **The owner decides, under its own authority.** It approves or declines the
  proposal with the amendment action, from its conversation or its recurring
  pass. The harness records the decision on the proposal under that role's
  authority, exactly as it records the operator's command, and tells the
  operator afterwards. Nobody asks him first.
- **A decline says what would change the answer.** Its reason is what the
  proposer reads, so it states what would have to be true for the proposal to
  be approved.
- **An approval writes nothing.** The owner then writes the revision of its
  document in the ordinary way, and that revision lands through its own
  reviewed run. Until it does, the approved change is still the owner's to
  write.
- **An undecided proposal stays the owner's.** It counts in the age of the
  amendment queue on the owner's list. It never moves to the operator because
  it has waited.
- **What stays the operator's.** A change to the brief or the goals recorded
  as `intent: fundamental`, or recorded without saying which it is, is his to
  decide, reason `fundamental-intent`; the Lead Product Manager drafts it and
  argues it, and the amendment action refuses it to every role. He may also
  decide any proposal himself, from the command line or the dashboard, and his
  decision replaces the role's.
- **What is refused.** A role deciding a proposal against a document another
  role owns is refused, naming the owner.

Rejected: putting every proposal to the operator, which routes an approval to
him that is not his, the defect this design exists to end; and having the Lead
Product Manager decide every proposal, which gives her the designs and
decision records that are the architect's, and the work definitions that are
the development manager's.

## The operator's closed list

The operator is the owner only for one of these reasons, named as the code
names them:

- `fundamental-intent`: a change to what the goals admit or refuse, drafted by
  the Lead Product Manager, or an amendment to the brief or the goals that does
  not say which kind it is.
- `credential`: a credential or an external account only a person holds.
- `repository-setting`: a forge or repository setting the harness's token
  cannot change.
- `protected-file`: a file beyond every grant, which today means Claude Code's
  settings files and activating a role definition.
- `human-gate`: a step an item reserved for a person with `human-gate:`.
- `own-hold`: lifting a hold or pause the operator placed himself.
- `diverged-history`: saying which history is right when a target branch
  diverged from the forge, which needs rights on the protected branch that no
  role holds.

The earlier revision of this design called two of these `forge-setting` and
`beyond-grant`; the reasons are the same and only the names changed to match
the code. The list lives in `internal/ownership` as a closed set. Adding a
reason is an amendment to the invariant
[the-operator-owns-only-what-only-he-can-do](../decisions/invariants/the-operator-owns-only-what-only-he-can-do.md),
never a code change on its own.

## A role's decision that needs the operator's hand

Some remedies are a role's to decide, but the harness cannot yet perform them
for that role. Today these are applying operational configuration, which
waits on configuration applied through a run (yoyodyne-ifd.434.8), and
committing a document written from a conversation where its kind's approval
policy is not automatic (yoyodyne-ifd.433.21). Such an entry keeps the role as
its owner and carries `hand`: the operator, the capability that is missing,
and the item that closes the gap. It never sets the owner to the operator.

The needs-a-human line prints two heads from this:

- **Yours to decide**: entries the operator owns, each with its reason.
- **A role's decision that needs your hand**: entries carrying `hand`, each
  naming the role that decided, what to type, and the item that ends the need.
  They are counted as a harness defect, not as the operator's queue.

## The escalation ladder

Every escalation names a role, and each rung goes to the one above it:

1. The developer, through a stopped run or a raise that the item cannot be
   met, goes to the development manager.
2. The development manager goes to the Lead Product Manager on the item's
   intent, and to the architect on its design.
3. A program manager goes to the Lead Product Manager for anything outside its
   lane.
4. The architect goes to the Lead Product Manager on anything touching intent.
5. The Lead Product Manager is the top rung among the roles.

What the Lead Product Manager cannot act on is one of two things. It is either
on the closed list, and the operator owns it with that reason. Or it is a
missing capability, and she admits a defect naming the capability and the
entry that met it, under the tool interface or the capability that would close
it. It is never an ask of the operator. The development manager's `escalate`
decision, the Lead Product Manager's report handling, and every role's ask of
the operator are refused when they name the operator without a reason from the
closed list, and the refusal names the rung to use instead.

## How the operator's capabilities shrink

The capabilities only the operator held are moved to roles one at a time,
through the registered actions and tools of
[the tool interface](tool-interface.md). Each one that moves removes an entry
kind from the hand head. Already moved: a role writing its own documents from
a conversation (yoyodyne-ifd.100), the Lead Product Manager ending a directive
(yoyodyne-ifd.430.32) and withdrawing her own proposal (yoyodyne-ifd.433.18),
the development manager stopping a run (yoyodyne-ifd.428.42), and owning roles
deciding proposed changes to their own documents (yoyodyne-i92, designed under
yoyodyne-ifd.437.14). In order, next: operational configuration applied
through a run (yoyodyne-ifd.434.8); a document write from a conversation
confirmed under the approvals policy for its kind, so that only goals changing
intent ask the operator; and committing that write through a reviewed run
rather than by hand.

## What is enforced in code

- The registry covers every entry kind: a test fails on a kind with no rule.
- For every kind, a test drives fixture records through the rule and fails
  when the operator is returned without a reason from the closed list.
- The operator's mover value is named only in `internal/ownership`: a test
  over the module's non-test Go files fails on any other use.
- Escalations, report handlings, and asks naming the operator without a
  closed-list reason are refused.
- An entry with `hand` never renders under "Yours to decide".
- Deciding a proposal is refused to every role but the owner of the document it
  targets, and to every role for a change to the brief or the goals recorded as
  `intent: fundamental` or with no intent.

The existing tests stay: the renders of every owner's label
(yoyodyne-ifd.432.23), every persona and contract carrying the
decide-and-report rule (yoyodyne-ifd.430.21), and critical reports reaching the
Lead Product Manager at once (yoyodyne-ifd.430.27).

## Alternatives rejected

- **Fixing each surface where it misroutes.** That is what was done, and new
  code kept adding places that decide.
- **Keeping the operator as the fallback and auditing it.** The development
  manager's pass over what waits on the operator (yoyodyne-ifd.430.31) does
  that, and it finds misroutes after they have reached him. It stays as a
  second check, not as the mechanism.
- **Setting the owner to the operator while a capability is missing.** That
  tells him a decision is his when it is not, which is the defect this design
  exists to end. The hand field says what is true: the role decided, and the
  harness needs his keyboard until an item lands.

## Build

Three bounded items, admitted by the Lead Product Manager:

1. The registry, the resolver, the three tests, and every surface reading the
   resolved owner, removing the owner logic each surface derives today.
2. The hand field and the two heads on the needs-a-human line, the dashboard,
   and Slack.
3. The refusals of operator-targeted escalations, handlings, and asks without
   a closed-list reason, with the refusal naming the rung to use.

Owning roles deciding proposed changes to their own documents was built
separately (yoyodyne-i92).
