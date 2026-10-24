---
id: follow-ups
kind: design
title: 'Follow-ups: every wait has an owner, a check the harness runs, and two deadlines'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-24T20:15:11.200419Z
      reason: 'yoyodyne-ifd.432.25.20: the operator''s direction of October 24 that follow-ups work mechanically. Every wait writes a follow-up with an owner, a check from a closed list the harness runs, and two deadlines from configuration; it ends only when the check passes or with a recorded reason; lapses go back to the owner, then the owner''s manager, and past the second deadline are recorded as bugs and never pruned; the operator''s assistant uses the same store through yoyo follow-up. Absorbs re-checking holds a command can confirm (yoyodyne-ifd.432.25.19) and sending factory-stopping steps only a person can take to Slack (yoyodyne-ifd.432.25.18), names how existing waits are brought in, and names the build pieces.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-24T20:15:11.200419Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1641, for document-1641.1 under the automatic approval policy
---
# Follow-ups: every wait has an owner, a check the harness runs, and two deadlines

## What this is for

Serves the autonomy goal: the operator states intent and answers what only he can answer, and the roles run the rest. That only holds if a wait one role leaves for another is picked up again without somebody remembering it. This week several were not:

- the development manager waited to hear how a trial run of the hold on new work went, and nothing told her;
- the conditions on scoped local race checks (yoyodyne-ifd.429.46) waited six days for her confirmation;
- asks to the operator went unanswered and nothing came back to the role that asked;
- the Xcode license on the operator's Mac was not accepted from about 17:45 PDT on October 23 to 11:49 PDT on October 24, every check failed, and the factory stood still for about 19 hours, because the step reached him only as one line on a list and nothing checked whether it had been done.

The operator asked on October 24 for follow-ups that work mechanically (yoyodyne-ifd.432.25.20). This design says what a follow-up is, what creates one, what ends one, what happens when one is late, and how the waits that exist today are brought in.

## What it rests on, and what was not read

Read at commit 1eb444f4: [who owns what](ownership-and-the-operator.md), [stopped run recovery](stopped-run-recovery.md) and the management-loop protocol, and the items for the one registry of waiting entries (yoyodyne-ifd.432.25), re-checking a hold whose cause a command can confirm (yoyodyne-ifd.432.25.19), a step only a person can take reaching the operator on Slack (yoyodyne-ifd.432.25.18), and the hold on new work being decided again on every development manager pass (yoyodyne-ifd.432.25.8, closed and landed).

Not read: the code in `internal/ownership`, `internal/readmodel`, `internal/triage`, the code that parks an item, report handling, and the Slack sink. A developer building a piece below reads the code it changes first, and where the code does something this design does not account for, the piece stops and the architect is asked.

## The decision

A **follow-up** is one record. Every act that leaves something waiting writes one, in the same act. It carries:

- **who waits**: the role, or the operator, that is waiting for the outcome and is told when it comes;
- **owner**: who has to act for the check to pass, resolved by the registry in [who owns what](ownership-and-the-operator.md), so the operator is the owner only for a reason on his closed list;
- **what it is about**: the item, run and stop, report, exchange, pull request, document or hold it concerns, by identifier;
- **what it blocks**: the items it holds back, and whether it holds intake or leaves developer slots empty;
- **check**: one check from the closed list below, with its arguments;
- **two deadlines**: when it goes back to its owner, and when it goes to the owner's manager;
- **origin**: the role, the harness, the operator, or the operator's assistant;
- **state**: open, passed (with the time the check passed), or closed (with who closed it and the reason).

Follow-ups are kept in one store in the harness's state directory, written through the confined writer of [repository-confined writes](repository-confined-writes.md). The registry gains one entry kind, `follow-up`, whose rule returns the recorded owner. An entry of another kind that has a follow-up, such as a hold, a report or a stopped run's wait, is shown once, as its own kind, with the follow-up's check, deadlines and age beside it. There is no second list: the read model builds what each surface shows from the registry, as before.

## What creates a follow-up

Each of these acts is refused unless it writes its follow-up in the same act. The refusal names the missing field.

- **Parking an item.** The parking role names the check that would release it, most often another item closing.
- **A wait decided on a stopped run.** The development manager's wait decision in [stopped run recovery](stopped-run-recovery.md) names its release condition as a check from the list below. This is the existing "wait until this closes, then back on the list" entry, made general.
- **A hold on new work**, whoever places it: the hold the development manager decides, the harness's pause for lack of provider capacity, a hold for an environment cause such as a license not accepted, and a hold or pause the operator places himself.
- **Asking the operator.** A report that names him for a reason on his closed list writes a follow-up he owns, with the check that shows the step was done (below).
- **A report addressed to a role, or to nobody.** The follow-up's owner is the role it is for, or the Lead Product Manager; its check is that the report's handling is recorded.
- **An ask from one role to another that is still open when the asking reply ends.** Its check is that the exchange is closed.
- **A request from the operator or his assistant**, through `yoyo follow-up add` (below).

A sentence in a note such as "please confirm" is not a wait, and nothing reads it as one. A role that needs something from another records a follow-up or asks; the role contracts and personas say so.

## The checks

A check is chosen from a closed list kept in the harness's own code, as every capability is. A role never writes a command for the harness to run.

- An item is closed; an item is no longer parked; an item has a run that landed on the target branch.
- A pull request is merged.
- A stopped run's stop has left the development manager's list, as step 2 of stopped run recovery says.
- A report's handling is recorded.
- An exchange between roles is closed.
- A proposed change is decided.
- A document has landed on the target branch.
- A file exists at a path in the repository, at the target branch's current commit.
- A hold has been released.
- A provider can be used again, by its reset passing or a successful check of the provider.
- **An environment check supplied by the harness**, for a stop cause it recognises: a license accepted (`xcodebuild -license check` passing), a tool found on the search path, a login still valid. The harness attaches it to the hold or stop when it records the cause. This takes in re-checking a hold whose cause a command can confirm (yoyodyne-ifd.432.25.19).
- **A command check the project declares by name** in `follow_ups.command_checks` in `.yoyodyne/config.yaml`, each with a command and a time limit. A follow-up names the check, never the command. It runs with what a project's configured check may do and nothing more.

There is no check that is only a time. A wait for a clock no one controls uses the provider check above, and every other wait that time alone would end is one whose deadline brings it back.

## What ends a follow-up

A follow-up leaves the open list in only these ways:

- **Its check passes.** The harness records the time and the result, closes the follow-up, and tells whoever waits: a role's next pass or turn is told, and the operator is told in the channel and his summary. Where the follow-up belongs to a hold the harness may lift, the harness releases that hold, records that it did and why, and says so in the channel and on `yoyo status`.
- **Its owner closes it with a reason**, such as "no longer needed, because the item it waited on was retired". A close with no reason is refused.
- **Whoever waits withdraws it with a reason**, because they no longer need the outcome.
- **The operator closes it**, from the command line or the dashboard, as an override.

Being read, acknowledged, or answered with "I will look" ends nothing.

The harness runs every open follow-up's check every `follow_ups.check_interval` (default 5 minutes). It runs command checks and environment checks every `follow_ups.command_check_interval` (default 10 minutes), one at a time. It records a result only when the result changes. A check that cannot be run counts as not passed and shows why. A check that cannot be run three times in a row is shown as a degraded part of the harness, which the harness owns.

## Deadlines, and what happens when one passes

The deadlines are set when the follow-up is written, from `follow_ups.deadlines` in `.yoyodyne/config.yaml`. They are chosen by the priority of the highest-priority item the follow-up blocks. A follow-up that holds intake or leaves every developer slot empty counts as priority 0, whatever it is about. The defaults:

| Priority of what it blocks | Back to its owner after | To the owner's manager after |
| --- | --- | --- |
| 0 | 1 hour | 4 hours |
| 1 | 4 hours | 24 hours |
| 2 and lower | 24 hours | 72 hours |

The development manager sets these numbers, as she sets the other numeric limits under section 6 of [portable agent configuration](portable-agent-configuration.md). A role writing a follow-up may set earlier deadlines than these, never later ones. The existing setting for a trial run of the hold on new work, `execution.brake_probe_overdue_after` (default 45 minutes), stays, and is that follow-up's first deadline.

**Past the first deadline**, the follow-up goes back on its owner's list of things to look at, ahead of the owner's other entries of the same priority. It shows:
- what it is about and who waits;
- the priority of what it blocks, the items it holds back, and how many developer slots are empty because of it;
- how long it has been open, in the operator's local time.

It stays there on every pass until it ends.

**Past the second deadline**, it is also put to the owner's manager, as the escalation ladder in [who owns what](ownership-and-the-operator.md#the-escalation-ladder) names one:

- the developer's to the development manager;
- a program manager's, the architect's, and the development manager's to the Lead Product Manager;
- the harness's own, and any owned by nobody, to the development manager;
- the Lead Product Manager's has no role above her. It is shown in the operator's regular summary as hers and late, and is never put to him as a decision.

A follow-up the operator owns is never escalated above him. Past its first deadline it is sent again as the next section says, and past its second the Lead Product Manager is shown it, to check whether a role could take the step after all.

**Every lapse past the second deadline is recorded as a bug, and none is pruned.** The harness files it through the same intake as any filing, with its origin recorded as the harness and the autonomy goal named. It is filed against the role or the part of the harness that owned the follow-up. It names the follow-up, its owner, its deadlines, what it blocked and for how long. Its priority is 0 where the follow-up held intake or emptied every developer slot, and 1 otherwise, as the operating rule on reliability defects says. While a bug for the same owner and the same kind of check is open, a further lapse is added to it as a note rather than filed as a new item, so each lapse is recorded and no run is spent on a duplicate. The follow-up and its lapse are kept for good.

## A step only a person can take, when it stops the factory

Where the operator owns a follow-up and it holds intake or leaves every developer slot empty, the harness posts it in the channel and sends him a direct message at once. The message says in ordinary words:
- what is stopped;
- since when, in his local time;
- the exact step he has to take.

It repeats every `follow_ups.person_reminder_interval` (default 30 minutes) while the follow-up is open. When the check passes, a last message says the stop has cleared. A follow-up he owns that stops nothing is shown under what is his to decide and in his summary, posted in the channel once, and not sent directly.

This is how a step only a person can take reaches the operator on Slack (yoyodyne-ifd.432.25.18). Finding why no Slack message went out on October 23 and 24 stays that item's own work.

## The operator's assistant

The operator, or his assistant acting as him on his machine, records a follow-up with `yoyo follow-up add`. It names:
- the role that owns it (the Lead Product Manager unless another is named);
- a check from the list;
- what it is about.

Its origin is recorded as the operator or his assistant, and its deadlines come from configuration. The owner is never the operator.

`yoyo follow-up list` prints every open follow-up, and those closed in the last seven days with their reasons. It can be narrowed to one origin, one owner, or what is late. The assistant rebuilds what it was following by reading that list after a reset, rather than from its own memory. `yoyo follow-up close` closes one with a reason and is the operator's override.

## Bringing in the waits that exist today

The first start of the build that carries the store writes a follow-up for each wait that exists then, once, and records that it did.

- **Parked items.** An item parked on an open item it depends on gets the check that item closed. Any other parked item gets the check that it is no longer parked, owned by the role that parked it, or by the Lead Product Manager where no note names one. Each one therefore comes back to its owner within its first deadline to be given a real check or closed with a reason.
- **Open asks.** An exchange still open gets the check that it is closed, owned by the role asked. A report naming the operator for a reason on his closed list gets the check for that step, owned by him.
- **Reports not yet handled** each get the check that the handling is recorded, owned by the role they are for, or by the Lead Product Manager.
- **Handled reports** whose handling names an item that answers them get the check that the item is closed, owned by whoever owns the item. A report answered by an item is then not lost when that item stalls. A handling that names no item writes nothing.
- **Waits on stopped runs** get the check their release condition names where it is on the list. Otherwise they get the check that the stop has left the list, owned by the development manager.
- **Holds** get their checks as the section on what creates a follow-up says.

Deadlines for these are counted from that start, not from when each wait began, so the store does not open with every entry already late; the age shown is counted from when the wait began. A follow-up written this way still files its bug on its first lapse past its second deadline, as any other does.

## Designs this changes

- **[Who owns what](ownership-and-the-operator.md)** gains the entry kind `follow-up` in its list of kinds and a table row: the owner the follow-up records, resolved by the same rules, the operator only with a reason from his closed list. It also gains a rule: an entry of another kind that has a follow-up is shown once, with its check and deadlines. That revision follows this one.
- **[Stopped run recovery](stopped-run-recovery.md)**: rule 1 gains one exception: a wait's follow-up lives in the follow-up store, and the run record names it. In step 2, a stop's wait keeps it off the list while that wait's follow-up is open, and the stop comes back when the follow-up's first deadline passes. That revision follows this one.
- **The hold on new work being decided again on every development manager pass** (yoyodyne-ifd.432.25.8) stays as built. Its follow-up adds the deadlines and the escalation to the Lead Product Manager.
- **Working alongside other contributors**: the store is kept on each harness's machine. A follow-up is seen only on the machine that recorded it, even where it is about a shared item. Carrying follow-ups between machines is left for that design to settle.

## What is enforced in code, and what is not an invariant

- A test lists every act that leaves something waiting, as named above, and fails where one can be recorded without its follow-up.
- The registry's test that every entry kind has a rule covers `follow-up`, and its test that the operator is never returned without a reason from the closed list covers follow-ups too.
- A test fails where a follow-up is closed without a reason, or ends by any way not listed above.
- A test fails where a follow-up carries a command rather than a check's name.

No invariant is added. Every way to break these rules goes through the writers and the registry this design names, and their tests hold them. If a wait is later found that is recorded somewhere none of them reaches, that is the case for making this an invariant.

## Alternatives rejected

- **A list of follow-ups beside the registry**, as the item warned against: two lists would disagree about who owns a wait.
- **Keeping each follow-up on the record it is about**, the run record or the item or the report. Waits with no such record, such as the assistant's requests, would then need a store anyway. Checking and deadlines would be built once for each kind of record.
- **Follow-ups as tracker items**: each would enter the backlog and the pull order, and a five-minute check would write to the tracker constantly.
- **Letting a role write the command a check runs**: that would give every role a way to run commands through the harness, which configuration and roles must never grant.
- **A check that is only a time**: that is the wait that got lost this week; the deadline is what time is for.
- **Filing a new bug for every lapse**: a recurring lapse would file the same item again and again. One open bug per owner and kind of check, with a note for each lapse, keeps every lapse without duplicates.
- **Escalating the operator's own follow-ups to a role**: no role can take a step on his closed list. A reminder to him, and the Lead Product Manager looking at whether a role could take it, are what can help.

## Build

These are the pieces; turning them into work items is the development manager's.

1. **The store and the record**: the follow-up record, its store in the state directory through the confined writer, the closed list of checks, the registry kind `follow-up`, and the tests on reasons and on the check list.
2. **Running the checks**: the interval runs, recording results that change, ending a follow-up when its check passes, telling whoever waits, releasing a hold the harness may lift, the environment checks the harness supplies, and the declared command checks. This takes in yoyodyne-ifd.432.25.19.
3. **The acts that must write a follow-up**: parking, the wait decision on a stopped run, every hold, asking the operator, reports, and an ask still open, each refused without its follow-up, with the test over all of them.
4. **Deadlines and lapses**: the configuration keys and defaults, a follow-up going back to its owner with its cost, the escalation past the second deadline, and the bug filed or noted on each lapse.
5. **What the surfaces show**: `yoyo status`, the dashboard, Slack, and each role's pass show follow-ups once, with check, deadlines and age. A step only a person can take that stops the factory is posted, sent and repeated as described. This takes in the sending half of yoyodyne-ifd.432.25.18.
6. **The commands**: `yoyo follow-up add`, `list` and `close`, for the operator and his assistant.
7. **Bringing in today's waits**, once, at the first start of the build.
8. **The role contracts and personas** say that a wait is recorded as a follow-up and that a sentence in a note is not one.

Piece 1 comes first. Pieces 2, 3 and 6 depend on it. Piece 4 depends on 2. Pieces 5 and 7 depend on 3 and 4. Piece 8 can go with 3.

## What a reviewer can check

- Every act listed under what creates a follow-up is refused without one.
- No follow-up names the operator as owner without a reason from his closed list.
- No follow-up ends except by its check passing or a close or withdrawal with a reason.
- No check runs a command a role wrote.
- A follow-up past its first deadline is on its owner's list on every pass until it ends, and one past its second is on the manager's list and recorded on a bug.
- A step only a person can take that holds intake or empties every developer slot reaches the operator in the channel and directly within one check interval, and is repeated until it clears.
