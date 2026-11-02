---
id: tracker-store-size
kind: design
title: 'Keeping the tracker store small: which writes change, when old history is squashed, and the limit the harness keeps'
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
---
# Keeping the tracker store small

The tracker store grows about 35 MB a day and was 930 MB on October 27. At that size an ordinary listing of open or ready items ran past its 30 second limit with no other process holding the store. Every role and every run reads the tracker, so a slow store slows the whole factory and turns ordinary reads into failures. This design says which writes change, when old history may be squashed, and the limit the harness keeps from now on.

It serves the autonomy goal: a factory whose reads fail on their own needs a person to clear it. It must not break the traceable chain: nothing here deletes an item or a note. It sits under the guarantee in [one owner for each guarantee](one-owner-for-each-guarantee.md) that reads of the harness's present state have one owner, and every piece below is built through the one tracker reader and writer that guarantee names.

## How this was decided

The ruling was due to rest on the figures of the measuring step that maintenance runs on a copy of the store. That step landed on October 28 and records, on a copy only, the size of the store and the time of the open listing, the ready listing and a single read at four points: as it is, after `bd gc`, after `bd compact --days 30`, and after `bd flatten`, with a full export compared before and after each. Its first real run had not happened when this was written. Rather than leave the store growing while waiting, this design decides now and lets the first figures choose between branches that are already decided. Nothing below needs a further ruling once the figures exist.

The measuring step also established that the harness's own code never reads the store's history; the only reader is one conformance test. That is what makes squashing old history safe for the harness.

## Writes that stop or change

1. A note saying a recurring pass left an item is written the first time a pass leaves it, and again only when one of these happens: the reason given changes; the same reason is given a second time in a row; or the same reason is given for the first time after the pass has been told to decide the item or ask about it. Those two later notes carry wording the first note does not, which the rules for an item a pass keeps leaving promise, so an unchanged reason writes at most three notes. Every other repeat with the same reason is counted in the harness's own state and shown wherever the note is shown, as "left again N times since" with the time of the latest. One ruling item collected about fifty such notes in three days; each was a write to the store and kept forever in its history.
2. The automatic export no longer runs inside every write. The tracker reader and writer writes the export at most once every five minutes when something has changed, and always before any commit that carries the export, so what is committed is never older than the store.
3. The dashboard's open and ready listings come from one shared listing refreshed at most once a minute, not from a tracker read on every page refresh. A reader that fails keeps the last good listing and its time, as the dashboard already does for its other sections.

Rejected: removing notes the harness writes, or merging old notes into one. Notes are append only by the operator's standing rule, and the chain of reasons runs through them.

Rejected: one note per reason with no later notes. The later stages of an item a pass keeps leaving tell the reader something the first note cannot, and a count alone would hide when the pass was told to decide.

## Old history

Items and notes are never deleted or rewritten. What may be removed is the store's record of past versions, which nothing the harness or the chain of reasons reads.

- The live store may be squashed with `bd compact --days 30` only when all four of these hold:
  1. the latest measurement on a copy shows every item and note unchanged across that squash;
  2. maintenance alone holds the store;
  3. the live store has first been copied, with that copy kept for 14 days and its place recorded in the harness's state;
  4. the squash's time on the measured copy is no longer than the watch session's redeploy drain bound, and no developer run is in flight when the squash starts.
- How the fourth condition is met. Once maintenance decides a squash is due, the scheduler starts no new developer run until the squash ends or is skipped; a repair or a continuation counts as a new developer run. A run already in flight counts as in flight whatever step it has reached, review included. The squash waits for runs already going for at most the redeploy drain bound. If runs are still in flight at that bound, no squash runs, new starts resume, and maintenance files one warning for the development manager naming the step and how many runs were still going. The hold on new starts ends on every way the step ends, including a failure partway, and it also ends on its own at the drain bound plus the measured squash time, so a maintenance pass that dies cannot keep it.
- When the squash's time on the measured copy is longer than the redeploy drain bound, no squash runs, and maintenance files one warning for the development manager naming the step and the measured time. A squash too long for that bound is a question for the architect, not something to work around.
- Deciding whether a squash is due lists and counts no work items. It uses a measure the store answers within its time limit at any size, such as the store's size on disk, and takes it before the hold on new starts begins. A listing fails on the same slowness the squash exists to cure: on October 31, 2026, at 19:43 PDT, the count taken before a squash was stopped at its 30 second limit with the store at 1,033 MB, so a start that depended on a listing would stop the squash ever running once the store grew large enough.
- The squash's record states how long it held the store.
- `bd flatten`, which squashes all history, is used only when the 30 day squash does not bring the store within the limit below, and under the same four conditions.
- If the comparison shows any item or note changed, neither squash is run on the live store, and maintenance files a warning for the development manager naming the step and the item that changed.

The fourth condition was ruled on October 30, 2026, on the program manager's report bf5be847, and completed in exchange 492bd009 on November 1, which settled how new starts are held off before a squash begins.

Rejected: letting the squash run while developer runs are in flight. The squash holds the store's exclusive lock for its whole length, so on a store near 1 GB the scheduler's and the runs' tracker reads would time out at 30 seconds while it ran, and a run could fail partway over nothing it did.

Rejected: waiting for runs in flight with no bound. New starts are held while the squash waits, so an unbounded wait would leave developer slots idle with ready work waiting; the redeploy drain bound is the wait the factory already accepts.

Rejected for now: moving the store to the tracker's server mode. It is allowed only once it is shown that a developer run cannot reach the server, and that check belongs to the separate measuring item under the same parent. This design neither adopts nor refuses it.

## The limit the harness keeps

Listing open items and listing ready items each finish within 10 seconds on the live store.

- The weekly measurement checks the limit. When either listing takes longer, or the store has doubled in size since its last squash, maintenance runs the 30 day squash at its next pass under the conditions above.
- If the store is still over the limit after the squash, maintenance files one warning for the development manager with the figures, and does not repeat it until the figures change.
- Raising the tracker read wait to 120 seconds stays as the protection for ordinary reads until this limit holds for two weekly measurements in a row.

## Which branch the first figures choose

- If the listings are within the limit after `bd gc` alone, the nightly cleanup already in place is enough with the write changes above, and no squash is run.
- If they come within the limit only after the 30 day squash, the live store is squashed at the next maintenance pass.
- If they come within it only after `bd flatten`, the live store is flattened at the next maintenance pass.
- If none of the three brings them within the limit, the current data itself is too large: the write changes above are the remedy, and the weekly warning keeps it in front of the development manager.

## Build pieces

Each is admitted under the work that gives reads of the harness's present state one owner, and is bounded on its own:

1. A note that a pass left an item is written once per reason, and again only at the two later stages of the same reason described above, so at most three notes per unchanged reason; every other repeat is counted and shown.
2. The automatic export moves out of each write into the tracker reader and writer, at most every five minutes and before any commit that carries it.
3. The dashboard's open and ready listings come from one shared listing refreshed at most once a minute.
4. A maintenance step squashes the live store under the conditions above, keeping a copy for 14 days.
5. The weekly measurement checks the 10 second limit and the doubling, starts the squash, and files the one warning.

## Assumptions

These are what this design takes to be true; a measurement that shows otherwise changes the branch, not the decision.

- Nothing outside the harness reads the store's history, beyond the one conformance test.
- Repeated notes and the export inside each write are a large share of the growth.
- `bd compact` and `bd flatten` keep every item and note, as the comparison on the copy will show.
- A shared listing for the dashboard exists or can be built on the one tracker reader.
- Ten seconds leaves room under every caller's wait.

This is a design decision, not an invariant: a change that gets it wrong slows the factory but breaks nothing outside its own scope.
