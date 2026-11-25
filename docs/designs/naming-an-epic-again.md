---
id: naming-an-epic-again
kind: design
title: 'Epic mode rulings: naming an epic again, turning epic mode back on, and how the remaining paths ask the one decision'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-25T07:47:45.114564Z
      reason: Replaces document-3919.1 whole, for yoyodyne-9k1y.7.1. Point 6 on role passes now says a refused summons after an automatic hold is kept only by the watching session and lost when it starts again. Adds the ruling that delivering a stopped run to the development manager is a prompted pass that asks first and stays outstanding when refused, built in yoyodyne-9k1y.7.2. The other rulings are unchanged; the checks are written afresh because the earlier closing sections could not be read.
    - action: amended
      by: architect
      at: 2026-11-25T08:38:01.115992Z
      reason: 'yoyodyne-9k1y: adds the rulings on the team branch update, yoyo review and repeated merge requests, checked against the code (reports 1308f3b6 and 97a88ba2); says a queued change is left off the list of unpublished pull requests only while an update is due and epic mode refuses it (report c4106115); replaces the out of date line on when yoyo epic start stops being refused. The other rulings are unchanged.'
    - action: amended
      by: architect
      at: 2026-11-25T10:08:33.901255Z
      reason: 'yoyodyne-9k1y: corrects point 5. Turning epic mode off does not itself clear a refused team branch update; it clears when the update begins or none is due, as internal/maystart/refusals.go at 9e1fc663f6b1 does and as the published first part says. The other points are unchanged.'
    - action: amended
      by: architect
      at: 2026-11-25T21:02:02.825815Z
      reason: 'yoyodyne-9k1y.19: rules what saved fact shows the list of unpublished pull requests that an update is due. It is the refused start, which names its run and is taken away by a check that finds no update due or cannot tell. The run''s record stays untouched. Point 4 is reworded to match and point 6 and its checks are added; the other paths are unchanged.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-25T07:47:45.114564Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3957, for document-3957.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-11-25T08:38:01.115992Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4008, for document-4008.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-25T10:08:33.901255Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4017, for document-4017.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-25T21:02:02.825815Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 4058, for document-4058.1 under the automatic approval policy
---
# Epic mode rulings: naming an epic again, turning epic mode back on, and how the remaining paths ask the one decision

Epic mode (yoyodyne-9k1y) lets the operator name the epics the factory works on. This record rules on questions the first part of its design leaves open. It serves the autonomy goal: the operator's act is the authorization, it must be possible to give it again, and nothing may start outside it.

The first question: the design does not say whether an epic that has ended may be named again, or how epic mode comes back on after `yoyo epic off` while an epic has not ended. The developer of the record (epic mode piece 1, yoyodyne-9k1y.2) reported this on November 24, 2026, and had the record refuse naming any epic a second time until it was ruled on.

The second question: the developer and the reviewer of the first half of the wiring (epic mode piece 5 first half, yoyodyne-9k1y.6.1) reported on November 24, 2026 that two ways a run starts are missing from the closed list of start paths. The developer of the third part (yoyodyne-9k1y.6.3) reported three more the same day.

The third question: the developer of the first half of piece 6 (yoyodyne-9k1y.7.1) asked the same day what is kept of a refused document, a refused role pass, a refused review of unmet goals and a refused reading of messages, what clears it, when a role pass asks, and what the correction turn is.

The fourth question: the reviewer of that piece reported the same day (report-cc5f60c215ffcec0befcbe165a503361) that delivering a stopped run to the development manager starts a turn and has no named path.

The fifth question: the developer of the second half of piece 6 (yoyodyne-9k1y.7.2) reported the same day (report-4d2af4fbe59a343edd1f378897f6bd1a) that bringing the target branch up to date with the team's branch and `yoyo review` both start work and have no named path, and asked whether repeating a merge request does.

## Decision: naming an epic again

1. **The record holds one entry for each naming, not one for each epic.** This replaces the words `one entry for each epic ever named` in the first part. At most one entry for an epic is open. An entry that has ended is never changed or removed.
2. **An epic that has ended may be named again.** `yoyo epic start <item>` then writes a new entry: a new time, a new copy of the epic's description, and the list of open items it prints as the members. Nothing is carried over from the ended entry. An item is checked against other open epics as in a first naming.
3. **`yoyo epic off` ends no epic.** It turns epic mode off and leaves every open entry and its members as they are. It prints which epics are still open and the command that ends each.
4. **`yoyo epic start <item>` on an epic still open, while epic mode is off, turns epic mode back on.** It writes no new entry and changes no members. It prints the members, and any open item below the epic that is not a member, with the `yoyo epic add` command for each.
5. **Turning epic mode on brings back every open epic.** Whenever `start` turns epic mode on, whether it names a new epic or one still open, it prints every other open epic that is now worked on again, each with the command that ends it.
6. **`yoyo epic start <item>` on an epic still open, while epic mode is on, is refused.** The refusal says the epic is already open and gives the `yoyo epic add` command.
7. **`yoyo epic add` is refused for an epic with no open entry**, and the refusal gives the `yoyo epic start` command. `yoyo epic end` is refused there too, and says the epic has already ended or was never named. Both work on an open epic while epic mode is off, so the operator can settle what is open before turning it back on.

`start` is still refused while any path that starts work is not wired, in all of these cases.

## Decision: two more paths

Both paths join the closed list in `internal/maystart/paths.go`, so `yoyo epic start` is refused until both are wired. Each is judged by the rule for a run of a work item. The decision is asked beside the existing checks and replaces none of them.

1. **The run that resolves a conflict with the team's branch.** `Pipeline.ResolveTeamBranchConflict` in `internal/orchestrator/teambranchresolve.go` asks about the item the development manager named, beside its call to `holdIntake`, before a run is reserved. A refusal, or a decision that could not be made, is a refusal that clears by itself, as the hold on new work is there. The update waits and asks again at its next check, the development manager's decision is kept, and no round is spent.
2. **The run that brings in a pinned outside file.** `Pipeline.BringInOutsideFile` in `internal/orchestrator/outsidefilelanding.go` asks about the work item the record is for, which is `landing.For`, never the bookkeeping item the run is recorded under. It asks beside its call to `holdIntake`, before anything is fetched. A refusal spends no attempt, starts no wait, stops no record and files no report. `NextOutsideFile` asks too, and passes over a record that is refused, so a record that may not start never keeps a later one from starting. A license file brought in with a file is judged with that file. A decision that could not be made passes the record over for that pull.
3. **A run that brings a file in and has already started carries on to its end**, when epic mode is turned on under it or the harness restarts. It is an exception to the wait that piece 5 gives other runs. It holds no developer slot and starts no developer session. At most one runs at a time, so one left waiting would keep every other record from starting, a member's among them.
4. **Neither path is exempt at its start for finishing earlier work.** That sentence, given in the revision the third part of piece 5 (yoyodyne-9k1y.6.3) was admitted from, speaks of starting a run only. A conflict run or an outside file run not yet started is refused even where it would finish something an earlier run began. It takes nothing from point 3. A conflict run that has already started is not an exception: it waits as any other run does under piece 5, because it holds a developer slot.
5. **A refused start is recorded by the one shared writer** the part that records refused starts builds (yoyodyne-9k1y.6.4), on both paths, as on every other. That record is not a report, and writing it spends nothing.

## Decision: three paths that put an ended run back to work

Each of these makes a run that had ended live again, takes a developer slot, and pays for checks or a review. Each is on the closed list, is judged by the rule for a run of a work item, and asks about the item its run is for. None is exempt as a change already in the merge queue: that exception is for an entry the harness's own merge queue is landing, or a merge GitHub can make by itself.

1. **Bringing a change queued on GitHub up to date.** `Reconciler.updateQueuedHead` in `internal/orchestrator/queuedchecks.go` asks after the run is retired or found impossible to replay, and before its check of the hold on new work. Those two earlier steps start nothing and stay as they are. A refusal, or a decision that could not be made, leaves the run's record, the item and the pull request exactly as they are: the merge is not withdrawn, and it is not handed on as stuck however long it has waited. The check returns without reaching the limit on a queued merge. The next one asks again. A merge GitHub already dropped waits the same way. A merge still queued may land by itself if its checks come to pass, because its head is the one that was approved.
2. **Bringing a change that waits for a person's merge up to date.** `Reconciler.updatePersonMerge` in `internal/orchestrator/personmerge.go` asks after its refusal of a run that cannot be put back, and before its check of the hold on new work. A refusal, or a decision that could not be made, is one more reason the wait is kept as it stands: nothing is withdrawn, nothing is noted on the item, and the next check asks again. A merge or a closing by the person is still settled while the update is refused.
3. **Taking up the checks of a run stopped because GitHub refused the harness's login.** `GitHubIdentityStops.takeUpPublishing` in `internal/orchestrator/githubidentity.go` asks beside its check of the hold on new work, before the item is noted or claimed. A refusal, or a decision that could not be made, leaves the run stopped, its entry on the development manager's list open, and its count of attempts as it was. It never ends the look: unlike the hold on new work or a full set of slots, it is about this one item, so the next run is still looked at.
4. **The list of unpublished pull requests says the same.** While the decision refuses the update of point 1 or 2, and point 6 shows that the update is due, `stuckPublication` in `internal/orchestrator/triage.go` does not list that change to the development manager as an unpublished pull request, however old it is. It reads the answer with `Decide`, which keeps nothing, never with `Ask`. A decision that could not be made lists the change as today. A change whose pull request is closed, or whose merge failed for a cause other than the update being refused, is listed as today. Status still shows the refused start.
5. **A change is left off that list only while an update is due and the decision is what keeps it from being made.** A queued change for which no update is due, such as one whose head is level with its target and whose merge was not dropped, is stuck for another reason and is listed as today. So is one whose checks on GitHub could not be read, because the harness then cannot tell whether an update is due. Epic mode may hide only a wait that it causes.
6. **The refused start is what shows that an update is due.** The list reads `not-started.json`, and that file is the only saved fact it uses for this. It leaves a change off only where all of these hold: the file can be read; it has an entry for the item, under the path of point 1 or 2 that fits the change; that entry names this run; the entry was last refused no more than an hour ago; and `Decide` refuses the update now. Otherwise the change is listed as today.

### How that entry is kept true

- **It names its run.** An entry under either of the two paths gains an optional field with the identifier of the run it is about, given by the check that was refused. The schema version stays 1. There is still one entry for each item and path. A refusal for another run of the item puts that run in its place, and the entry is written at once when the run changes. An entry that names no run hides nothing.
- **It is taken away when no update is due.** A check of that run that finds no update due takes away the entry that names the run. So does one that cannot tell whether an update is due, and one that finds the run cannot be put back. For a queued change this covers checks that pass or are still running, a head level with its target, a failure that is the change's own, checks that could not be read, and a run retired or impossible to replay. For a person's merge it covers a head level with its target, a failed reading of how far behind it is, a merge in progress on GitHub, a request that is no longer open, and a run that `cannotUpdatePersonMerge` refuses. An entry that names another run is left as it is. This adds to the ways an item's entries are cleared that [the first part](epic-mode-first-part.md) lists.
- **A check that stops before it can ask leaves it.** An example is a check in a process that hosts no runs, which has already found the update due. Such a check neither makes an entry nor takes one away.
- **A dropped merge is judged by the check that looks at it.** Where that check finds an update due and asks, the refusal makes the entry as for a merge still queued. Where it finds another cause, it takes the entry away. The reading of GitHub's checks kept on the run is not consulted by the list, because it may be from before the merge was dropped.
- **There is one writer.** The removal is a function of `internal/maystart/refusals.go`, beside `KeepUpdateOf`, under the same lock. A check that would remove nothing writes nothing. A failed write or removal is reported, changes nothing else, and is tried again by the next check. The hour in point 6 is what bounds how long an entry that could not be removed may hide a change. It also means that a change may be listed after the harness was stopped for longer than an hour, until its next check, and that is accepted.
- **The run's record is not used.** Points 1 and 2 leave it as it is.

The look of point 3 also resumes an approved change stopped at its landing. That is finishing an interrupted merge, already on the list, and it asks where every resumption asks. A refusal there does not end the look either.

Once one of the first two has put a run back, the run has started. `Reconciler.ContinueUpdates` in `internal/orchestrator/queuedchecks.go` then hosts it. That is picking up a waiting or interrupted run, so it asks under that path, and a run outside the members waits, gives back its slot, and keeps its claim, branch and checkout. The attempt to bring a handed back change up to date is hosted there too, and asks first under its own path.

Each refused start is recorded by the same shared writer.

### Rejected for the list

- **Saving each refused check's reading on the run.** It would be a second saved answer to the same question, it would change a record that points 1 and 2 leave alone, and status would still show a refused start after no update was due.
- **Asking GitHub while the list is built.** Building the list would then wait on GitHub, and the check has already asked.

### What a reviewer can check of points 4 to 6

- For a queued change and for a person's merge, outside the members and past the listing age: refused with an update due, it is left off. After a check finds its head level it is listed, and the entry is gone. After a check whose reading fails it is listed, and the entry is gone.
- A dropped merge whose update is due and refused is left off.
- A change is listed where it has no entry, where its entry names no run or another run, where the entry was last refused more than an hour ago, and where the file cannot be read.
- One left off is listed once its item is added or epic mode is turned off.
- A check that hosts no runs changes no entry, and a check of one run takes away no entry of another.
- Building the list writes nothing, and the run's record is the same before and after a refused check apart from what a check records today before it asks.

`internal/maystart/refusals.go` and `internal/orchestrator/personmerge.go` were read whole for points 4 to 6 at main aca8fae72a05, with the first 16226 of the 52589 bytes of `internal/orchestrator/queuedchecks.go`. The rest of that file, with `updateQueuedHead` and what becomes of a dropped merge, was not read, and neither were `internal/orchestrator/triage.go` and the code of `refuseRun`. Where they differ, the builder stops and says so for the architect.

## Decision: what a role pass asks, and when

1. **Find the pass due, ask, then claim.** Before this is built, the claim is also the check that a pass is due, and it moves the schedule. The check that a pass is due is split out as a read that changes nothing, which the claim also uses; the claim keeps its own check, because another process may claim in between. The decision is asked only when every other check would let the pass start, and before anything is claimed, marked as read or delivered. `Trigger.Start` in `internal/orchestrator/recurringfirings.go` makes every such claim, so it asks in its one loop, after its check of provider capacity. It covers a recurring task, a needs-design wake, a delivery of critical reports, the review of unmet goals and each pass of a program manager, scheduled, for an event or requested. `Trigger.Fire` makes the same claims and asks the same way.
2. **A refusal changes nothing but the record of refused starts.** It takes no place among the passes running at once, holds no conversation, and moves no schedule, cursor, request, label record or report. The next pass in the order is still looked at.
3. **A refusal is counted once each time the pass comes due, not once a pull.** The entry keeps, for each thing that can start the pass, the last time it was counted for: for a schedule, the time it fell due; for events, requests and reports, the identifier of the oldest one outstanding; for a label, the item and the change that added the label. The same one is not counted again, after a restart either. That value and the count are one write. It is written again only where the reason changed.
4. **A pass the decision refuses is not a missed pass.** The check that records a pass as missed asks `Decide` and records and reports nothing while it refuses. Once the pass is allowed, lateness is counted from the first pull that allowed it, which the watching session keeps in memory. After a restart it is counted from that session's start.
5. **The path each asks under.** A recurring task, and a program manager's schedule, ask as a role's recurring pass. An event, a label, a request, a delivery of critical reports and the summons after an automatic hold ask as a prompted pass. The review of unmet goals asks under its own path only, before the goals are read, although it is delivered as a pass of the Lead Product Manager. A refused review does not keep that role's ordinary pass from starting.
6. **The summons after an automatic hold.** The hold itself is placed whatever the answer. The scheduler's summon step asks before it launches the turn. A refused summons of the development manager is kept, in the watching session, as the waiting summons that `summonWaiting` makes again at each pull while that hold stands, counted once for each hold. It is lost when the session starts again, and that is accepted: no pass request is saved, no durable record of the summons is asked for, and no test that it survives a restart is asked for. The guide says it is lost. Losing it neither lifts nor hides the hold. The hold stays on status until a named epic is open, when the development manager's passes may start again.
7. **The correction turn is a prompted pass.** The turn that corrects a refused tracker block is not a recurring pass, as the code that launches it says, and belongs to no claim. It asks before `launchTurn`, and is counted once for each refused block. A refusal leaves the correction outstanding. If it cannot stay outstanding, the path is left not wired and is reported.
8. **A pass already claimed takes all its turns**, if epic mode is turned on under it.
9. **Reading outside messages asks before the reader runs.** A refusal reads no message, starts no query and moves no query's time.
10. **The attempt to repair a handed back change asks before its run is adopted.** A refusal writes nothing to the run, hands nothing on and spends no attempt.

In all of these a decision that could not be made is a refusal.

## Decision: delivering a stopped run to the development manager

This is built inside the second half of piece 6 (yoyodyne-9k1y.7.2), not in the first half.

1. **It is a prompted pass**, and asks under that path, with the call the first half of piece 6 builds.
2. **It asks first.** It asks once, inside `Escalator.deliver` in `internal/orchestrator/escalate.go`, after a delivery is found due and before `Records.Attempt`, so before anything is claimed, recorded as delivered or sent to the provider. Every caller is then covered. If `deliver` does not start every such turn, it asks at the narrowest point they share, and before `Judge` in `internal/cli/escalate.go` where that starts a turn first. If one delivery takes several turns, it asks once.
3. **A refused delivery stays outstanding.** A refusal changes nothing about the run: the stop stays recorded and undelivered, the change is kept, and no attempt, failure, report or escalation is recorded. Refused and still due is an ordinary result, reported plainly. The existing record of an undelivered stop is the outstanding one. The delivery is tried again on the ordinary occasion and goes ahead when the decision admits it. If nothing tries it again after a restart, the existing recurring pass looks for stops still due.
4. **Deliveries still due are counted once for each stopped run**, however often they are tried, after a restart either.
5. **A turn already started finishes.** A turn begun again after a restart asks again. A reply already received is recorded without asking.
6. **It holds back nothing else.** Steps of the run that need no role's judgment do not wait for the delivery. Anything the judgment would start asks for itself. If a step of a live run that needs no judgment truly waits for the answer, the builder stops and says so for the architect.

A decision that could not be made is a refusal.

## Decision: the team branch update, `yoyo review`, and repeated merge requests

This was built by the last part of piece 6 (yoyodyne-9k1y.7.4). Both starts below are on the closed list in `internal/maystart/paths.go`.

1. **Bringing the target branch up to date with the team's branch is a path of its own** (`TeamBranchUpdate`). It is for a target branch and for no work item, so it is not judged by membership. With epic mode off it starts as before. With epic mode on it starts if at least one named epic is open, and is refused if none is, because a member's change lands on that branch. A decision that could not be made is a refusal. Every other hold still applies.
2. **It asks once an update is found due and before any work.** `teamBranchRun.update` in `internal/orchestrator/teambranchupdate.go` asks after it has found that the target does not carry the team's latest commit, and before it sets aside a waiting run, drops an earlier attempt, records a new one, builds, checks or reviews anything. Bringing the local copy of the target onto the remote's, earlier in that function, starts no work and is not asked about.
3. **What carries on without asking.** Following a pull request already open for the update; landing a merge already checked and approved for the same two commits, while its commit is still held; and saying why a conflict or a refused merge still waits. None of these begins work. An approved merge whose commit is no longer held has to be built again, so it asks. A run that resolves a conflict asks under its own path.
4. **A refusal changes nothing that was kept.** The update stays due, the earlier attempt stays as it was, and no attempt, failure, report or escalation is recorded. The outcome says it is still due, why it was not started, and what clears it.
5. **A refused update is shown with the other refused starts**, counted once for each target branch however often it is tried. It is cleared when the decision lets the update begin, or when no update is due any more. An entry for another target branch is cleared too. Naming an epic, or turning epic mode off, does not clear it by itself: the update is still due until it is tried again, and the next check that begins it or finds none due clears it. An earlier version of this record said that turning epic mode off clears it; the code was read afterwards and is right. For this version the functions `note`, `settle` and `KeepUpdateOf` in `internal/maystart/refusals.go` were read at main 9e1fc663f6b1, after the account at the end of this record was written.
6. **`yoyo review` always starts.** It is a person's own command naming one branch, and it does not ask (`OperatorReview`, marked as always starting). It is on the closed list so that the check over the whole module knows of it, and nothing the harness starts by itself may use that path.
7. **Asking GitHub again for a merge already approved starts no work** and has no path. The last part of piece 6 was given this to prove by a test.

## Decision: what is kept of a refused start

The record stays in `internal/maystart/refusals.go`, with one writer. The new fields are optional and its schema version stays 1. A failed write is reported and never permits a start.

- **Publishing a document** asks in `publishDocumentAttempt`, for a new run only, beside its call to `holdIntake`. A refusal there leaves the document confirmed and waiting, with nothing counted against it, and a scheduler looking for the next waiting document passes over it. A document run already started carries on to its end.
- **A refused document has one entry of its own**, by document identifier, whether or not its revision names a work item. The entry has the path, the write it is about, the items it answers, the reason, and when it was first and last refused. It takes the place of the entries made before for each item a document answers. A newer revision of the document updates it. It is cleared when a run of that document starts, when the decision would allow it, when the document is withdrawn or handed back, or when epic mode is turned off. It is not cleared because an item it answers closes. For a document that answers no item, the reason says that it waits until epic mode is turned off or it is written again naming a member.
- **A role pass has one entry for each role, agent and path**, with the count and the values of point 3 of the decision on role passes. The agent is empty except for a program manager, where it is the instance, so two instances never share a count. An entry written before this, by role alone, is a valid entry with no path or agent, and is cleared as today. Every entry of a role is cleared when a pass of that role is allowed, or when epic mode is turned off.
- **The review of unmet goals and the reading of outside messages keep nothing.** Status says whether each may start by asking the decision.

## Alternatives rejected

The list in the earlier text of this record could not be read when this one was written. These are the ones the rulings themselves support.

- **Asking after a role pass is claimed.** The claim moves the schedule, so a refused pass would be lost.
- **Counting a refusal at every pull.** The count would measure how often the harness looked, not how often work was held back.
- **Recording a refused pass as missed.** It would file reports about passes epic mode is refusing on purpose.
- **Saving a refused summons so that it survives a restart.** The hold it is about is already kept and shown, so a second lasting record would add a thing to keep in step for little gain.
- **Letting a refused delivery of a stopped run spend an attempt.** Attempts are limited, so epic mode alone would use them up and leave the stop undelivered.
- **Leaving an outside file run waiting once it has started.** It would keep every other record from starting, a member's among them.
- **Refusing the team branch update whenever epic mode is on.** The members' own changes land on that branch, and it would fall behind the team's while they are worked on.
- **Making `yoyo review` ask.** A person asking for one review has already decided it.
- **Leaving off the list every queued change outside the named epics.** A change stuck for a reason epic mode did not cause would then be seen by nobody.

## What a reviewer can check

These checks were written afresh from the decisions above and from the conditions for being done on the build items.

Naming an epic:

- an ended epic named again has a second entry and the first is unchanged; an epic never has two open entries;
- `off` then `start` on an open epic turns the mode on, writes no entry, changes no members and prints every other open epic; `start` while the mode is on is refused;
- `add` and `end` are refused for an epic with no open entry, and work on an open one while the mode is off.

Paths that start a run:

- each of the two more paths, and each of the three that put an ended run back to work, starts nothing for an item outside the members, spends nothing, and starts once it is allowed;
- a refused outside file record does not stop a later one; an outside file run already started carries on, and a conflict run already started waits;
- a refused update leaves the run, the item and the pull request unchanged, is not handed on as stuck, and is not listed as an unpublished pull request;
- a queued change outside the members with no update due, or whose checks could not be read, is listed as today;
- a refused login stop does not end the look at the others.

Role passes, reviews and messages:

- ten refused pulls with a restart in the middle count one refusal and move no schedule, and lose no event, request, label or report; a second time due counts two; once allowed the pass starts once and its entry clears;
- a refused pass is not recorded as missed, and a pass after it in the order starts at the same pull;
- two program manager instances have two entries, and a scheduled and a prompted refusal of one role have two;
- a refused summons starts once allowed in the same watching session, the hold stands and shows on status meanwhile, and the guide says a refused summons is lost when the session starts again; nothing asks that it survive;
- a refused correction turn starts once allowed, or the path is reported as not wired;
- a refused review of unmet goals, and a refused reading of messages, leave their stores unchanged, and status says whether each may start;
- a refused attempt to repair a handed back change leaves its run record identical.

Delivering a stopped run:

- with every named epic ended, no provider call is made, and the run, its attempt counts and its kept change are unchanged;
- refusals repeated across a restart count one for that run, and two stopped runs count two;
- after an epic opens, or epic mode is turned off, it is delivered once and the count clears;
- with an epic open or the mode off it works as before, and a refused delivery does not stop a change in the merge queue;
- a restart during a turn asks again, and a received reply is recorded without asking.

The team branch update and `yoyo review`:

- with epic mode on and no epic open, an update found due sets nothing aside, records no attempt and runs no check or review, and repeated checks count one for that target branch;
- it starts once an epic is open or the mode is off, and its entry clears then or when no update is due;
- an open pull request is followed, and an approved merge still held is landed, while a start would be refused; an approved merge no longer held is not built again while refused;
- `yoyo review` starts with every named epic ended;
- repeating a merge request for an approved change starts no run, check or provider call.

Documents and the closed list:

- a document answering no member stays confirmed and waiting with nothing counted against it, has one entry, appears once on status, and the next waiting document is still looked at;
- `yoyo epic start` is refused while any path on the closed list is not wired. Every path on it was wired when this version was written.

## What this rests on

Read at main f29e37efdefc for this version: `internal/maystart/paths.go`, whole, and the first 49152 of the 58817 bytes of `internal/orchestrator/teambranchupdate.go`. The ruling on the team branch update was first given without reading that code. What was read agrees with it. The rest of that file, where the merge a conflict run leaves is asked about, was not read, and neither was the code that keeps the refused update or the code of `yoyo review`. Point 5 of that decision follows what was asked of the last part of piece 6 (yoyodyne-9k1y.7.4) and the calls that were read. The test on a repeated merge request was not read.

Point 5 of the decision on an ended run was ruled from a reviewer's account (report-c41061159b8799db5861e37e440214fa). The code of the list as it landed (yoyodyne-9k1y.6.6) was not read, and by that account it leaves off more than the point allows.

Read at main 4b8bcdcd9132 for an earlier version: `internal/orchestrator/escalate.go`, whole. The ruling on delivering a stopped run was first given without reading the code; that file agrees with it. `deliver` records an attempt with `Records.Attempt` before it calls `Judge`, so the question goes ahead of that.

The earlier account of the ruling on role passes says it was made from `Trigger.Start` and what surrounds it, and did not read the rest of `recurring.go`, `schedule.go`, `triage.go` or the reader of messages.

Not read: `internal/cli/escalate.go` and the code of any other path named here. Where the code differs from a ruling, the builder stops and says so for the architect.
