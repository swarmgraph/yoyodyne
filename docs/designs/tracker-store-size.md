---
id: tracker-store-size
kind: design
title: 'Keeping the tracker store small: which writes change, when the store is rebuilt from its export, and the limit the harness keeps'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-10-28T22:32:58.588586Z
      reason: Rules how the tracker store's growth is stopped and old history squashed safely, and the limit the harness keeps, deciding every branch the first measurement can show so the ruling no longer waits on it.
    - action: amended
      by: architect
      at: 2026-10-31T17:07:54.780943Z
      reason: 'amendment-b1f077c4, approved by the architect: the note that a pass left an item is also written at two later stages of an unchanged reason, the second leave in a row and the first leave after the pass was told to decide or ask, so at most three notes per unchanged reason; the first write change and build piece 1 now say so, matching what was built for the once-per-reason note.'
    - action: amended
      by: architect
      at: 2026-11-02T09:02:57.428178Z
      reason: 'amendment-d69a4e86, approved by the architect: the squash''s fourth condition as built for the squash timing item, from the October 30 ruling on report bf5be847 and exchange 492bd009. New starts are held once a squash is due, runs in flight are waited for up to the redeploy drain bound, the hold also ends on its own, a squash too long for the bound is skipped with a warning, and the start decision lists no work items.'
    - action: amended
      by: architect
      at: 2026-11-17T23:13:00.761721Z
      reason: 'yoyodyne-ifd.428.126.4.20: rules on rebuilding the store from its own export in place of squashing its history, which the diagnosis on yoyodyne-ifd.428.126.4.19 measured frees no space; removes the wait for runs in flight and the hold on new starts, which stopped every live squash, because the rebuild is bounded under the tracker read wait; rules on the backup folder; and names the build work the fix that brings the store under its limit carries. Supersedes the squash conditions of amendment-d69a4e86 and amendment-b9c01fd4.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-10-28T22:32:58.588586Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1916, for document-1916.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-31T17:07:54.780943Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2050, for document-2050.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-11-02T09:02:57.428178Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2118, for document-2118.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-17T23:13:00.761721Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2974, for document-2974.1 under the automatic approval policy
---
# Keeping the tracker store small

The tracker store grew about 35 MB a day and was 930 MB on October 27. At that size an ordinary listing of open or ready items ran past its 30 second limit with no other process holding the store. By mid November it was about 1.56 GB, listings took up to two minutes, and maintenance's tracker check timed out. Every role and every run reads the tracker, so a slow store slows the whole factory and turns ordinary reads into failures. This design says which writes change, when the store is rebuilt from its own export, what happens to the tracker's backup folder, and the limit the harness keeps from now on.

It serves the autonomy goal: a factory whose reads fail on their own needs a person to clear it. It must not break the traceable chain: nothing here deletes or changes an item, a note, a dependency, a label or a comment. It sits under the guarantee in [one owner for each guarantee](one-owner-for-each-guarantee.md) that reads of the harness's present state have one owner, and every piece below is built through the one tracker reader and writer that guarantee names.

## How this was decided

The first version of this design, on October 28, ruled that old history be squashed with `bd compact --days 30`, or `bd flatten` if that was not enough, under four conditions, the fourth of which held new developer starts and waited for runs in flight up to the redeploy drain bound.

The fix that brings the store under its limit (yoyodyne-ifd.428.126.4.19) then measured all three on copies of the live store, and recorded the figures in its diagnosis, `docs/diagnoses/yoyodyne-ifd-428-126-4-19-store-stays-over-limit.md`:

- neither `bd compact --days 30` nor `bd flatten` frees disk space: a copy grew from 1,498 MB to 1,605 MB;
- rebuilding a fresh store from the store's own export brought a copy to 14 MB;
- the fourth condition stopped every live squash, because on a busy factory runs are always in flight at the drain bound.

The squash is therefore replaced by a rebuild from the export, and the wait for runs in flight is removed. This revision was ruled on November 17, 2026, on the reviewer's report 1bf27f21 and the developer's report a7fed71a.

The measuring step established earlier that the harness's own code never reads the store's history; the only reader is one conformance test. That is what makes a rebuild, which keeps no history, safe for the harness.

## Writes that stop or change

1. A note saying a recurring pass left an item is written the first time a pass leaves it, and again only when one of these happens: the reason given changes; the same reason is given a second time in a row; or the same reason is given for the first time after the pass has been told to decide the item or ask about it. Those two later notes carry wording the first note does not, which the rules for an item a pass keeps leaving promise, so an unchanged reason writes at most three notes. Every other repeat with the same reason is counted in the harness's own state and shown wherever the note is shown, as "left again N times since" with the time of the latest. One ruling item collected about fifty such notes in three days; each was a write to the store and kept forever in its history.
2. The automatic export no longer runs inside every write. The tracker reader and writer writes the export at most once every five minutes when something has changed, and always before any commit that carries the export, so what is committed is never older than the store.
3. The dashboard's open and ready listings come from one shared listing refreshed at most once a minute, not from a tracker read on every page refresh. A reader that fails keeps the last good listing and its time, as the dashboard already does for its other sections.

Rejected: removing notes the harness writes, or merging old notes into one. Notes are append only by the operator's standing rule, and the chain of reasons runs through them.

Rejected: one note per reason with no later notes. The later stages of an item a pass keeps leaving tell the reader something the first note cannot, and a count alone would hide when the pass was told to decide.

## Rebuilding the store from its export

Items, notes, dependencies, labels and comments are never deleted or rewritten. What a rebuild drops is the store's record of past versions, which nothing the harness or the chain of reasons reads.

### When it runs

A rebuild is due when the store is larger than 1 GB on disk, or when the weekly measurement finds either listing over the 10 second limit below. Deciding that it is due lists and counts no work items: it reads the store's size on disk, which the store answers at any size. A listing fails on the same slowness the rebuild exists to cure: on October 31, 2026, at 19:43 PDT, a count taken before a squash was stopped at its 30 second limit with the store at 1,033 MB.

### First, on a copy

Each time a rebuild is due, maintenance first does the whole rebuild on a copy of the live store:

1. copy the live store;
2. export the copy;
3. build a fresh store from that export;
4. export the fresh store and compare it with the export in step 2;
5. record how long steps 2 to 4 took, and the fresh store's size.

The comparison must find every item, note, dependency, label and comment unchanged. If any differs, no rebuild runs on the live store, and maintenance files one warning for the development manager naming the step and the first item that differs.

If steps 2 to 4 took longer than 60 seconds, no rebuild runs on the live store, and maintenance files one warning for the development manager with the measured time. A rebuild too long for that bound is a question for the architect, not something to work around.

### Then, on the live store

When the copy passes, maintenance rebuilds the live store, through the tracker reader and writer:

1. take the tracker's write lock, which every write the harness makes for a role or a run already goes through;
2. export the live store;
3. move the live store aside as the kept copy, and record its place and time in the harness's state; it is kept for 14 days and then removed by maintenance;
4. build a fresh store from the export in step 2;
5. export the fresh store and compare it with the export in step 2;
6. if they match, put the fresh store in the live store's place; if they differ, put the kept copy back in its place, and file one warning for the development manager naming the step and the first item that differs;
7. release the write lock.

The lock is released on every way the step ends, including a failure partway, and a failure partway always ends with either the fresh store or the kept copy in the live store's place, never neither. The rebuild's record states how long it held the lock, the store's size before and after, and which way it ended.

### No wait for runs in flight

The rebuild does not wait for runs in flight, and the scheduler does not hold new starts for it. A role or a run that reads or writes the tracker while the lock is held waits behind it, as it waits behind any other write. That is safe because the rehearsed time is bounded at 60 seconds, and an ordinary tracker read already waits up to 120 seconds.

Rejected: keeping `bd compact` and `bd flatten`. They were measured to free no space, and a step that cannot shrink the store only adds risk.

Rejected: waiting for runs in flight, with or without a bound, and holding new starts while waiting. On a busy factory runs are always in flight, so the wait stopped every attempt, and the hold left developer slots idle with ready work waiting. Bounding the rebuild's own length under the read wait makes the wait unnecessary.

Rejected: rebuilding the live store without the rehearsal on a copy. The comparison on the copy is what shows the export carries everything before the live store is touched, and its time is what keeps the lock under the read wait.

Rejected for now: moving the store to the tracker's server mode. It is allowed only once it is shown that a developer run cannot reach the server, and that check belongs to the separate measuring item under the same parent. This design neither adopts nor refuses it.

## The tracker's backup folder

The tracker keeps copies of itself in `.beads/backup`, which had reached 3.8 GB.

- At each maintenance pass, copies older than 14 days are deleted. The newest copy is never deleted, whatever its age.
- The folder is never committed. The repository's ignore rules cover it, and the build piece checks they do.
- The tracker's own setting for how long it keeps copies is set to 14 days in this repository's tracker configuration, so the folder stops growing on its own. The build piece names that setting from the tracker's documentation; where the tracker has none, it says so, and maintenance's deletion alone keeps the limit.
- If maintenance finds anything in the folder that is not one of the tracker's own copies, it deletes nothing in the folder that pass and files one warning for the architect naming the file. What else may live there is a question for the architect.

The kept copy from a live rebuild is not one of these and is never placed in this folder.

## The limit the harness keeps

Listing open items and listing ready items each finish within 10 seconds on the live store, and the store stays under 1 GB on disk.

- The weekly measurement checks the limit. When either listing takes longer, or the store is over 1 GB, maintenance runs the rebuild at its next pass under the rules above.
- If the store is still over the limit after a rebuild, maintenance files one warning for the development manager with the figures, and does not repeat it until the figures change.
- Raising the tracker read wait to 120 seconds stays as the protection for ordinary reads until this limit holds for two weekly measurements in a row.

## Build pieces

Each is admitted under the work that gives reads of the harness's present state one owner, and is bounded on its own. Pieces 1 to 3 are as first ruled.

1. A note that a pass left an item is written once per reason, and again only at the two later stages of the same reason described above, so at most three notes per unchanged reason; every other repeat is counted and shown.
2. The automatic export moves out of each write into the tracker reader and writer, at most every five minutes and before any commit that carries it.
3. The dashboard's open and ready listings come from one shared listing refreshed at most once a minute.
4. The rebuild: the rehearsal on a copy and its comparison, the 60 second bound, the live rebuild under the write lock, the kept copy for 14 days and its removal, putting the kept copy back when the comparison fails, and the warnings. The fix that brings the store under its limit (yoyodyne-ifd.428.126.4.19) carries this piece, replacing the squash step it was built against, and stays open until the live store is under 1 GB.
5. The weekly measurement checks the 10 second limit and the 1 GB size, starts the rebuild, and files the one warning. Where the measurement still runs `bd compact` or `bd flatten` on its copy, those steps are removed.
6. The backup folder: deletion of copies older than 14 days except the newest, the ignore rule checked, the tracker's own setting set, and the stop and warning when something else is found.

A reviewer can check each piece against this design alone: the comparison covers items, notes, dependencies, labels and comments; nothing lists work items to decide a rebuild is due; no rebuild runs when the rehearsal exceeds 60 seconds; the live store is never left missing; and no step holds new starts or waits for runs in flight.

## Assumptions

These are what this design takes to be true; a measurement that shows otherwise is a question for the architect.

- Nothing outside the harness reads the store's history, beyond the one conformance test.
- The store's export carries every item, note, dependency, label and comment, as the comparison on the copy checks each time.
- Every write the harness makes to the tracker goes through the one tracker reader and writer and its write lock, since no role or run runs the tracker's command directly.
- A rebuild of the current store takes well under 60 seconds; the diagnosis's rebuild to 14 MB is the evidence, and the rehearsal checks it each time.
- A shared listing for the dashboard exists or can be built on the one tracker reader.
- Ten seconds leaves room under every caller's wait.

This is a design decision, not an invariant: a change that gets it wrong slows the factory but breaks nothing outside its own scope, and the comparison and the kept copy stand between a wrong change and lost work.