---
id: alpha-releases
kind: design
title: Releases go out first as alphas, with notes kept current and a list the operator tries by hand before the final tag
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-20T20:11:47.8801Z
      reason: 'yoyodyne-k3gj: the alpha releases design, written again after the first version was refused for one coined phrase. It covers alpha numbering, one notes file per version, notes kept current in the harness state after each landing with no role turn, the review list from fixed item notes, the operator''s mark as his own act, the final tag at the marked alpha''s commit, the wait that holds no slot, and seven build pieces.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-20T20:11:47.8801Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3223, for document-3223.1 under the automatic approval policy
---
# Releases go out first as alphas

Today every release tag is a final version, `vX.Y.Z`, and the next release's notes are drafted once and then go stale: `docs/releases/v0.6.0.md` was drafted on September 23 and never refreshed while hundreds of items closed after it. The operator asked on November 9 for a clear list of changes to try by hand before a release goes from alpha to its final tag. This design says how releases are numbered, how their notes stay current without a role spending a turn on it, where the review list lives and who writes it, how the operator marks it done, and how the release cut reads that mark.

It serves the goal of a traceable chain from the brief to verification: the final tag of a release names a commit somebody tried by hand, and the notes say which changes were tried. It is held to the standing goals of plain language and near autonomy: everything here runs without a person except the one step the operator keeps for himself on purpose.

It rests on the release scripts as they stand at main 451a37d7: `scripts/release-notes.sh`, `scripts/cut-release.sh` and `scripts/release-body.sh`, and on the releases guide, `docs/releases/README.md`. The part of the harness that runs the cut and records the tag was not read for this design; the build pieces name where its changes go.

## 1. Version numbers and alpha numbering

- A release is still decided by the Lead Product Manager and still has a version, `vX.Y.Z`, which she chooses as she does today.
- Before its final tag, a release is cut as one or more alphas: `vX.Y.Z-alpha.1`, `vX.Y.Z-alpha.2`, and so on, numbered from 1 with no gaps. The next alpha number is the highest alpha tag of that version on origin plus one.
- The final tag is `vX.Y.Z`, as today.
- The tag pattern in all three scripts widens to accept exactly one suffix: a literal `-alpha.` followed by a number with no leading zero. Nothing else is accepted, so the output of `git describe`, which the narrow pattern exists to refuse, is still refused.
- The previous release, which bounds what the notes cover, is the newest final tag the range can reach. Alpha tags are skipped when finding it, so every alpha's notes and the final notes cover everything since the last final release.
- The release workflow publishes an alpha's page marked as a prerelease, so the forge does not offer it as the latest release.

## 2. One notes file per version

- Each version has one notes file, `docs/releases/vX.Y.Z.md`, named for the final tag. Its alphas share it. Its heading names the version, not an alpha, so the final tag can name the same commit as the alpha it follows (section 6).
- The file keeps the three sections the releases guide already sets: key functionality, enhancements, bug fixes. Placing items in them stays the Lead Product Manager's judgement.
- A new section, **Try these by hand**, sits above the three (section 4).
- The readiness section the cut stamps stays where it is and is written as it is today.

## 3. Keeping the notes current without a role turn

The harness keeps the next release's notes current itself, with no provider call:

- Each time the harness confirms that a change has landed on the target branch, it redraws the next release's notes from the tracker's export, in the shape `scripts/release-notes.sh` draws today, and keeps that copy in the harness's state directory, not in the repository.
- The version it drafts for is the one named by the newest open release item. Where no release item is open, it drafts under the next minor version after the last final tag and says the version is not yet decided.
- `yoyo release notes` prints that copy, and the dashboard shows it on its delivery page with the time it was last drawn and the commit it covers.
- In the drafted copy each item takes one line, its title, its identifier and the goal it serves, so a release covering several hundred items stays well under the 125,000 characters a release page accepts. The item's full text stays in the tracker. Where the drafted copy would still pass the warning point `scripts/release-body.sh` uses, the copy says how many items it could not show and names the file in the repository.

The committed file on the target branch changes only when an alpha is cut: the alpha's release item has a reviewed run that takes the drafted copy, curates it, and lands it, exactly as a release's notes land today. Between alphas, the committed file is what the last alpha said, and the drafted copy is what is true now.

Rejected: committing the drafted notes to the target branch each time a change lands. Every such commit would need review by somebody other than its author and a place in the merge queue, roughly doubling the queue's traffic for a file nobody reads between alphas. Rejected too: drafting only when a release is cut, which is what left the v0.6.0 notes stale.

## 4. The review list

The review list is the **Try these by hand** section of the notes. Each entry names one change, with its work item, and one line saying what to try and what the operator should see. A change with nothing to try by hand stays in the notes and is left off the list.

The list is drawn from fixed notes on each work item, in the way the drafting script already reads an item's admission from its notes:

- The Lead Product Manager decides what goes on the list. She puts an item on it with a note beginning `On the release review list:`, followed by why, and takes it off with a note beginning `Off the release review list:`. The later of the two wins.
- The development manager writes the line on how to try each item on the list, as a note beginning `To try by hand:`. The latest such note is the line used.
- An item on the list with no such line is shown as waiting on the development manager, on her list and in the drafted notes.

Notes stay append only, as the operating rules require: putting an item on, taking it off, and rewording its line are each a new note.

## 5. How the operator marks the list done

- The operator marks the list done with `yoyo release reviewed vX.Y.Z alpha N`, or the matching button on the dashboard, naming the alpha he tried. It is recorded as his own act, the way his other decisions are recorded, with the alpha's tag, the commit it names, and the list as it stood at that commit. It is never recorded by editing the notes file.
- The harness refuses to put the review to him while any entry on the list lacks its line on how to try it, and tells the development manager which entries those are.
- Where he finds something wrong, he says so as he says anything else, and the release waits. A later alpha needs its own mark: a mark covers only the alpha it names.

## 6. The final tag

- The final tag is cut by the harness's release cut, as decided releases are cut today, under the same setting for approving releases.
- The harness reads the operator's mark before running the cut's checks. It cuts the final tag only where a mark exists, and it cuts it at the commit of the alpha the mark names. Nothing he did not try reaches the final tag. Changes that landed after that alpha belong to the next version, because its notes start from this final tag.
- The cut's checks run again on that commit as they do for any tag, and the release stays open until its tag is on origin.

The operator keeps this step for himself on purpose. It is not a routine approval for later work to remove.

## 7. Waiting for the operator

- While the final tag waits for his mark, the release holds no developer slot and no running run.
- The release item is shown as a step only the operator can take, on status, on the dashboard, and on the list of what waits on him, with the alpha to try and the entries on its list. Where a stored reason is needed, it is `human-gate`.
- Once his mark is recorded, the harness takes the cut up again on its own at its next pass, with no role turn and nobody asked to restart it.
- The operator's pause, where he has set one, stops this cut as it stops any other.

## 8. The releases guide

The releases guide, `docs/releases/README.md`, changes to say:

- that a release goes out as alphas before its final tag, how they are numbered, and that alpha pages are marked as prereleases;
- that one notes file serves every alpha of a version;
- where the current drafted notes are read between alphas;
- what the **Try these by hand** section is, and who adds to it;
- how the operator marks it done, and that the final tag names the commit of the alpha he tried.

## Build pieces

1. The three release scripts accept an alpha tag in exactly the shape of section 1, skip alpha tags when finding the previous release, and the release workflow publishes an alpha page as a prerelease. Tests cover an alpha tag, a refused version from `git describe`, and a range that skips alphas.
2. The drafting script renders one line per item when asked, and renders the review list from the three fixed notes of section 4, marking entries with no line on how to try them. A test holds the three note openings against what the harness's writers produce, as the test for the admission lines does today.
3. After each confirmed landing, the harness redraws the next release's notes into its state directory with no provider call. `yoyo release notes` prints them and the dashboard shows them, with the time drawn and the commit covered.
4. The operator's mark: the command and dashboard button of section 5, recorded as his act, refused while an entry lacks its line, and listed with the alpha and its entries on what waits on him.
5. The harness's release cut reads the mark for a final tag, cuts it at the marked alpha's commit, waits holding no slot and no run, and takes the cut up again on its own once the mark is recorded.
6. The Lead Product Manager's and the development manager's instructions say how each writes the fixed notes of section 4. Where those instructions live in a file only a person may change, this piece goes to the operator as a step only he can take.
7. The releases guide changes as section 8 says.
