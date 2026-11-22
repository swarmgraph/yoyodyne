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
    - action: amended
      by: architect
      at: 2026-11-19T12:22:19.100624Z
      reason: 'amendment-37cba8f5, approved by the architect: the live rebuild takes the tracker''s write lock first and new calls wait behind it while it waits, so overlapping calls cannot keep it out; only a process outside the harness, looked for after the lock is held, can stop it, and is named; the rehearsal copies under the lock and is never skipped because the live store is open; every due rebuild that does not go ahead is recorded with its reason, with one warning to the development manager after three in a row. Read whole at main a87840011938; only this section changes.'
    - action: amended
      by: architect
      at: 2026-11-22T02:26:15.471892Z
      reason: 'yoyodyne-ifd.428.126.4.26: rules on opening the tracker store once in bd''s server mode. Adopted only on four conditions checked first on a copy: speed under load, no developer run can reach the server, every bd on this machine goes through it, and dolt installed pinned by the harness''s setup. Otherwise, and meanwhile, listings of open and ready work come from one shared listing that any write clears. Adds build pieces 7 to 10. Read whole at main 848d8619071d.'
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
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-19T12:22:19.100624Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3074, for document-3074.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 5
      by: harness
      at: 2026-11-22T02:26:15.471892Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3370, for document-3370.1 under the automatic approval policy
---
# Keeping the tracker store small

The tracker store grew about 35 MB a day and was 930 MB on October 27. At that size an ordinary listing of open or ready items ran past its 30 second limit with no other process holding the store. By mid November it was about 1.56 GB, listings took up to two minutes, and maintenance's tracker check timed out. Every role and every run reads the tracker, so a slow store slows the whole factory and turns ordinary reads into failures. This design says which writes change, when the store is rebuilt from its own export, what happens to the tracker's backup folder, the limit the harness keeps from now on, and how the store is opened so that each call stops paying for it.

It serves the autonomy goal: a factory whose reads fail on their own needs a person to clear it. It must not break the traceable chain: nothing here deletes or changes an item, a note, a dependency, a label or a comment. It sits under the guarantee in [one owner for each guarantee](one-owner-for-each-guarantee.md) that reads of the harness's present state have one owner, and every piece below is built through the one tracker reader and writer that guarantee names.

## How this was decided

The first version of this design, on October 28, ruled that old history be squashed with `bd compact --days 30`, or `bd flatten` if that was not enough, under four conditions, the fourth of which held new developer starts and waited for runs in flight up to the redeploy drain bound.

The fix that brings the store under its limit (yoyodyne-ifd.428.126.4.19) then measured all three on copies of the live store, and recorded the figures in its diagnosis, `docs/diagnoses/yoyodyne-ifd-428-126-4-19-store-stays-over-limit.md`:

- neither `bd compact --days 30` nor `bd flatten` frees disk space: a copy grew from 1,498 MB to 1,605 MB;
- rebuilding a fresh store from the store's own export brought a copy to 14 MB;
- the fourth condition stopped every live squash, because on a busy factory runs are always in flight at the drain bound.

The squash is therefore replaced by a rebuild from the export, and the wait for runs in flight is removed. This revision was ruled on November 17, 2026, on the reviewer's report 1bf27f21 and the developer's report a7fed71a.

The measuring step established earlier that the harness's own code never reads the store's history; the only reader is one conformance test. That is what makes a rebuild, which keeps no history, safe for the harness.

The fix for slow listings (yoyodyne-ifd.428.126.4.24) then found, with the store small, that listings still ran past 10 seconds because every bd call pays for the store's change journal when it opens the store. Its figures are in `docs/diagnoses/yoyodyne-ifd-428-126-4-24-slow-listings.md`. Opening the store once, below, was ruled on November 21, 2026, on the reviewer's report 13a77d30 and the developer's report 37a0193e, for the ruling on server mode (yoyodyne-ifd.428.126.4.26).

## Writes that stop or change

1. A note saying a recurring pass left an item is written the first time a pass leaves it, and again only when one of these happens: the reason given changes; the same reason is given a second time in a row; or the same reason is given for the first time after the pass has been told to decide the item or ask about it. Those two later notes carry wording the first note does not, which the rules for an item a pass keeps leaving promise, so an unchanged reason writes at most three notes. Every other repeat with the same reason is counted in the harness's own state and shown wherever the note is shown, as "left again N times since" with the time of the latest. One ruling item collected about fifty such notes in three days; each was a write to the store and kept forever in its history.
2. The automatic export no longer runs inside every write. The tracker reader and writer writes the export at most once every five minutes when something has changed, and always before any commit that carries the export, so what is committed is never older than the store.
3. The dashboard's open and ready listings come from one shared listing refreshed at most once a minute, not from a tracker read on every page refresh. A reader that fails keeps the last good listing and its time, as the dashboard already does for its other sections. Build piece 10 widens this to every caller of those two listings.

Rejected: removing notes the harness writes, or merging old notes into one. Notes are append only by the operator's standing rule, and the chain of reasons runs through them.

Rejected: one note per reason with no later notes. The later stages of an item a pass keeps leaving tell the reader something the first note cannot, and a count alone would hide when the pass was told to decide.

## Rebuilding the store from its export

Items, notes, dependencies, labels and comments are never deleted or rewritten. What a rebuild drops is the store's record of past versions, which nothing the harness or the chain of reasons reads.

### When it runs

A rebuild is due when the store is larger than 1 GB on disk, or when the weekly measurement finds either listing over the 10 second limit below. Deciding that it is due lists and counts no work items: it reads the store's size on disk, which the store answers at any size. A listing fails on the same slowness the rebuild exists to cure: on October 31, 2026, at 19:43 PDT, a count taken before a squash was stopped at its 30 second limit with the store at 1,033 MB.

### First, on a copy

Each time a rebuild is due, maintenance first does the whole rebuild on a copy of the live store:

1. take the tracker's write lock, copy the live store, and release the lock, so the copy is of one consistent store and the lock is held only for the copying;
2. export the copy;
3. build a fresh store from that export;
4. export the fresh store and compare it with the export in step 2;
5. record how long steps 2 to 4 took, and the fresh store's size.

The rehearsal is never skipped because the live store is open: it works on a copy, and the copying waits for the lock like any other call.

The comparison must find every item, note, dependency, label and comment unchanged. If any differs, no rebuild runs on the live store, and maintenance files one warning for the development manager naming the step and the first item that differs.

If steps 2 to 4 took longer than 60 seconds, no rebuild runs on the live store, and maintenance files one warning for the development manager with the measured time. A rebuild too long for that bound is a question for the architect, not something to work around.

### Then, on the live store

When the copy passes, maintenance rebuilds the live store, through the tracker reader and writer:

1. take the tracker's write lock before anything else. Every tracker call the harness makes for a role, a run, the dashboard or the scheduler, read or write, goes through the one tracker reader and writer, and once the rebuild is waiting for the lock, a new call waits behind the rebuild rather than ahead of it. So calls that overlap one another cannot keep the rebuild out, and no call the harness makes ever stops a rebuild;
2. with the lock held, look for any process that has the store open. Because every call the harness makes is now waiting behind the lock, only a process outside the harness can be found. If one is, the rebuild does not go ahead this pass: the lock is released, and the step names that process by its command and process number;
3. export the live store;
4. move the live store aside as the kept copy, and record its place and time in the harness's state; it is kept for 14 days and then removed by maintenance;
5. build a fresh store from the export in step 3;
6. export the fresh store and compare it with the export in step 3;
7. if they match, put the fresh store in the live store's place; if they differ, put the kept copy back in its place, and file one warning for the development manager naming the step and the first item that differs;
8. release the write lock.

The lock is released on every way the step ends, including a failure partway, and a failure partway always ends with either the fresh store or the kept copy in the live store's place, never neither. The rebuild's record states how long it held the lock, the store's size before and after, and which way it ended.

Where the store is opened through bd's server, as ruled below, the live rebuild stops the server after step 2 and starts it again before step 8, so the store is never open to the server while it is moved, and the time the server was stopped counts within the rebuild's own bound.

### Every rebuild that does not go ahead is recorded

Each pass at which a rebuild is due and does not go ahead writes one entry to the rebuild record, with the time, the store's size, and the reason in plain words naming what stopped it. The reasons include: the comparison on the copy differed; the rehearsal took longer than 60 seconds; a process outside the harness had the store open, named; the lock was not had within the tracker's ordinary wait for a write; or any other failure before the live store was touched. A line in the pass's own output is not enough on its own.

After three such passes in a row, maintenance files one warning for the development manager naming what stopped each of the three. It files no second warning until the reason changes or a rebuild goes ahead. The warnings above for a comparison that differed or a rehearsal that ran too long are still filed when they happen; the count of three covers every reason.

### No wait for runs in flight

The rebuild does not wait for runs in flight, and the scheduler does not hold new starts for it. A role or a run that reads or writes the tracker while the lock is held, or while the rebuild is waiting for it, waits behind it, as it waits behind any other write. That is safe because the rehearsed time is bounded at 60 seconds, and an ordinary tracker read already waits up to 120 seconds.

The rebuild as first built skipped whenever another process had the store open, and recorded the skip nowhere. On a busy factory the dashboard, the scheduler and runs have the store open almost all the time, so it skipped every pass and the store stayed at about 1.64 GB on November 19, 2026, after the rebuild had landed. Taking the lock first, looking only for processes outside the harness, and recording every rebuild that does not go ahead were added on November 19, 2026, from the developer's proposal amendment-37cba8f5, raised on the fix that makes the rebuild take the lock first (yoyodyne-ifd.428.126.4.23), which carries this part of build piece 4.

A reviewer of that part checks, beside the checks under Build pieces: the live rebuild takes the lock before looking for any process with the store open; no call the harness makes stops a rebuild; the rehearsal is never skipped because the live store is open; every due rebuild that does not go ahead is in the rebuild record with its reason; and three in a row file exactly one warning.

Rejected: skipping the rebuild whenever any process has the store open. On a busy factory that is always, so the rebuild never runs and the store keeps growing.

Rejected: keeping `bd compact` and `bd flatten`. They were measured to free no space, and a step that cannot shrink the store only adds risk.

Rejected: waiting for runs in flight, with or without a bound, and holding new starts while waiting. On a busy factory runs are always in flight, so the wait stopped every attempt, and the hold left developer slots idle with ready work waiting. Bounding the rebuild's own length under the read wait makes the wait unnecessary.

Rejected: rebuilding the live store without the rehearsal on a copy. The comparison on the copy is what shows the export carries everything before the live store is touched, and its time is what keeps the lock under the read wait.

Server mode is ruled under Opening the store once, below.

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

## Opening the store once

### Why the store's size is no longer the cause

With the store small, listings still ran past 10 seconds. The diagnosis for slow listings found why. Dolt writes each change to a journal file beside the store until `bd gc` folds it in, and while a journal exists every bd process, a read included, rewrites the journal, its index and the store's manifest when it opens the store. Timed on a copy of the live store taken at 13:32 PST on November 21, 2026, with developer runs loading the machine:

| The copy | One item read | Listing not closed | Ready listing |
| --- | --- | --- | --- |
| as taken, 103 MB journal | 1.4 to 3.6s | 1.8 to 2.9s | 1.2 to 1.5s |
| folded, then 60 writes (2.8 MB journal) | 0.8 to 3.1s | 0.8 to 2.3s | 0.9 to 1.4s |
| folded, then one write | 0.6 to 1.2s | | |
| folded, no journal | 0.12 to 0.29s | 0.20 to 0.33s | 0.19s |

The harness writes every few minutes, so the store nearly always has a journal, and folding it more often does not help: a 2.8 MB journal cost nearly as much as a 103 MB one. With the harness's calls already served one at a time in the order asked, the line of calls was busy three quarters of every hour, and four fifths of a slow listing's time was its wait in that line. The cost has to be paid once, when the store is opened, rather than by every call. bd's server mode does that: one long running server holds the store open and every bd call is answered through it.

### The decision: server mode, on four conditions

The harness opens the tracker store through bd's server mode, provided all four conditions below hold. Each is checked before the live store moves. Where any fails, server mode is not adopted, the route under When server mode is not adopted carries the limit alone, and the reason is recorded in the slow listings diagnosis.

1. **It is fast enough, measured first.** Server mode has never been timed here. On a copy of the live store with a journal of at least 100 MB, with developer runs loading the machine, the server and direct opening are run side by side for eight minutes, each with the live mix the diagnosis used: four processes reading one item every 12 seconds and three listing every 30 seconds. Server mode is adopted only if no listing through it takes over 10 seconds and the median one item read through it is under half a second.
2. **No developer run can reach the server.** This is a safety boundary, not a matter of speed: no role or run reads or writes the tracker except through the harness. The server listens only on the machine's own local address, on a port the supervisor picks, behind a password the harness generates and keeps in its own state, outside every worktree and never placed in any run's environment. The measurement shows that a developer run, under the confinement it works in, is refused when it tries to connect to that port. If its confinement cannot refuse the connection, server mode is not adopted.
3. **Every bd on this machine goes through the server while it runs.** A bd that opened the store files directly beside a running server would be a second writer on one store. The measurement finds bd's own setting for reaching the store through its server, and the harness writes it in this machine's local tracker configuration, never in a committed file, so other contributors' clones are untouched. If bd has no such setting, server mode is not adopted.
4. **The harness installs `dolt`.** Server mode needs a `dolt` program this machine does not have. Installing a program the harness needs is harness work, not a step for the operator: the harness's setup installs `dolt` into the harness's own tools folder under its state directory, pinned to the version bd's documentation names for its server mode, and refuses a download whose checksum does not match. `yoyo doctor` reports `dolt` missing or at another version, naming the version wanted. Until it is installed the factory keeps opening the store directly, and nothing else in this design waits on it.

### Running the server

- **Who runs it.** The supervisor starts, stops and restarts the server together with the scheduler, the dashboard, Slack and maintenance, and moves it onto each new build with them, as the operating rule for the product's parts requires. It records the server's process number, port and start time in the harness's state.
- **Who calls it.** The one tracker reader and writer connects to the server for every call the harness makes. The line that serves the harness's calls in the order asked stays as it is; whether the server lets it be loosened is a later ruling, from measurements.
- **While it is down.** The tracker reader and writer opens the store directly, as it does today, only when the supervisor's record says the server is stopped and no server process is running. Where it cannot tell, such as a record that says running and a connection refused, it never opens the store directly: the call waits in the line for the server's restart up to the call's own bound, and then reads as unanswered. Before the supervisor starts the server it takes the tracker's write lock, so no direct opening by the harness is in progress when the server opens the store.
- **Programs outside the harness.** With the setting in condition 3, a person's bd and a program that shows the tracker on this machine go through the server too, and stop holding the store between the harness's calls. `yoyo doctor` reports any process outside the harness found holding the store's files directly, naming its command and process number.
- **What anyone can see.** `yoyo status` and the dashboard say how the tracker is reached: through its server since a time, or opening the store directly and why.
- **Undoing it.** The store's files are the same under either way of opening them, so stopping the server and removing the local setting returns to direct opening with nothing to mend.

### When server mode is not adopted, and meanwhile

However server mode turns out, the number of calls behind a listing comes down. The tracker reader and writer keeps one shared listing of open items and one of ready items. Any write it makes clears both, and the next caller of either reads it afresh; neither is ever more than 30 seconds old. Every caller of those two listings, the scheduler's pull, reconcile, status, the dashboard and the recurring passes, gets the shared listing, so callers within the same 30 seconds with no write between them cost no bd call. A claim, and any write, still reads the item it acts on itself first, so a listing up to 30 seconds old never decides what is changed. This is built now and does not wait on the measurement.

If, with this in place and server mode adopted or refused, either listing still runs past 10 seconds in two weekly measurements in a row, the architect looks at the limit again with those figures. Nothing raises the limit before then.

Rejected: folding the journal with `bd gc` after every few writes. A small journal was measured to cost nearly as much as a large one, and the fold itself holds the store.

Rejected: a bd that does not rewrite the journal on every read. It would be a change to bd, and replacing or forking the tracker is outside v1.

Rejected: putting the server's address in the committed tracker configuration. It would move every contributor's clone onto a server their machine does not run.

Rejected: letting a developer run reach the server and relying on it not to. No role or run reads or writes the tracker except through the harness, and that holds by what a run can reach, not by its behaviour.

## Build pieces

Each is admitted under the work that gives reads of the harness's present state one owner, and is bounded on its own. Pieces 1 to 3 are as first ruled.

1. A note that a pass left an item is written once per reason, and again only at the two later stages of the same reason described above, so at most three notes per unchanged reason; every other repeat is counted and shown.
2. The automatic export moves out of each write into the tracker reader and writer, at most every five minutes and before any commit that carries it.
3. The dashboard's open and ready listings come from one shared listing refreshed at most once a minute.
4. The rebuild: the rehearsal on a copy and its comparison, the 60 second bound, the live rebuild under the write lock, the kept copy for 14 days and its removal, putting the kept copy back when the comparison fails, and the warnings. The fix that brings the store under its limit (yoyodyne-ifd.428.126.4.19) carries this piece, replacing the squash step it was built against, and stays open until the live store is under 1 GB.
5. The weekly measurement checks the 10 second limit and the 1 GB size, starts the rebuild, and files the one warning. Where the measurement still runs `bd compact` or `bd flatten` on its copy, those steps are removed.
6. The backup folder: deletion of copies older than 14 days except the newest, the ignore rule checked, the tracker's own setting set, and the stop and warning when something else is found.
7. The measurement of server mode, first: on a copy of the live store with `dolt` installed by hand in a scratch folder, conditions 1, 2 and 3 are each checked as written above, and the figures and the outcome, adopted or not with the condition that failed, are recorded in the slow listings diagnosis. It changes nothing on the live factory.
8. Only if piece 7 adopts server mode: the harness's setup installs `dolt`, pinned and checked by checksum, and `yoyo doctor` reports it missing or at another version.
9. Only if piece 7 adopts server mode, after piece 8: the supervisor runs the server with the product's other parts; the tracker reader and writer connects through it and opens the store directly only while the server is known to be stopped; the local setting from condition 3 is written; the live rebuild stops and starts the server within its bound; doctor names outside processes holding the store's files; status and the dashboard say how the tracker is reached.
10. Now, independent of pieces 7 to 9: the shared listings of open and ready items in the tracker reader and writer, cleared by any write it makes and never more than 30 seconds old, used by every caller of those listings, with claims and writes reading their item afresh. It widens piece 3.

A reviewer can check each piece against this design alone: the comparison covers items, notes, dependencies, labels and comments; nothing lists work items to decide a rebuild is due; no rebuild runs when the rehearsal exceeds 60 seconds; the live store is never left missing; no step holds new starts or waits for runs in flight; server mode is never adopted without the figures for all four conditions recorded; no developer run is given the server's address or password; the store is never opened directly while the server might hold it; and no write is decided from a shared listing.

## Assumptions

These are what this design takes to be true; a measurement that shows otherwise is a question for the architect.

- Nothing outside the harness reads the store's history, beyond the one conformance test.
- The store's export carries every item, note, dependency, label and comment, as the comparison on the copy checks each time.
- Every write the harness makes to the tracker goes through the one tracker reader and writer and its write lock, since no role or run runs the tracker's command directly.
- A rebuild of the current store takes well under 60 seconds; the diagnosis's rebuild to 14 MB is the evidence, and the rehearsal checks it each time.
- A shared listing for the dashboard exists or can be built on the one tracker reader.
- Ten seconds leaves room under every caller's wait.
- bd's server mode reads and writes the same store files as direct opening, so the export and the rebuild are unchanged by it; piece 7 checks this by comparing exports taken each way.

This is a design decision, not an invariant: a change that gets it wrong slows the factory but breaks nothing outside its own scope, and the comparison and the kept copy stand between a wrong change and lost work. The one rule here a change could break outside its own scope, that no run reaches the tracker except through the harness, is already held by the operating rules and by condition 2.