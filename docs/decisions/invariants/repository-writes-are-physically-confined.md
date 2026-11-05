---
id: repository-writes-are-physically-confined
title: Harness-owned writes are physically confined to their declared root
status: active
established_by:
    - yoyodyne-ifd.136
    - yoyodyne-ifd.437.31
    - yoyodyne-ifd.437.26
revisions:
    - action: created
      by: architect
      at: 2026-08-23T04:09:56.923059Z
      reason: Recorded while promoting the repository-confined-writes brief into governed designs under the operator's 2026-08-22 mandate; the one constraint of the brief's five that binds work which never mentions it.
    - action: amended
      by: architect
      at: 2026-11-05T15:47:29.730279Z
      reason: 'Brings the invariant up to the repository-confined-writes design as revised on October 14 and 20: names the three roots and every write under the state directory, adds directories moved out of the root and files that gain a link outside it, and records that no existing file but a lock file is changed in place. The additions waited until invariants could be sent from this conversation. The rule''s earlier meaning is unchanged; ''check-to-use race'' is put in plain words.'
---

## Must hold

Every write the harness owns passes through the shared write primitive and declares the root it is confined to: the repository, a harness-managed worktree, or the harness's state directory, including every write under the harness's state directory. No write changes anything that physically resolves outside that root, whether by following a symbolic link, by a symbolic link that points at nothing, by a path replaced between the check and the change, by a held directory moved out of the root, or by writing to a file that has gained a link outside it. No existing file is changed in place, except a lock file: new content is published as a new file. No caller opts out, and no code outside the approved low-level package writes directly to a file under any of these roots.

## Why

Checking a path as a string was proven insufficient by confirmed escapes in the artifact, invariant, and initialization writers: symbolic links are legal repository content, so a path string never proves where a write lands. A later review found two more ways out that a check made only before writing cannot close, because a directory can be moved and a file can gain a link after the check; so a held directory is checked on both sides of every change, and content is published as a new file that no later link can reach. The state directory is held to the same rule because the harness owning a directory does not stop a link inside it from pointing out. Containment held by each writer is containment each new writer can silently lack; one primitive with no opt-out is a boundary a reviewer can find violations of by looking for writes that bypass it.
