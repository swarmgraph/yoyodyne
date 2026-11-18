---
id: developer-run-scratch-outside-git
kind: decision
title: Developer run scratch folders live in the state directory, outside the repository's .git directory
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-16T21:34:31.331575Z
      reason: 'yoyodyne-ifd.435.70: records the ruling of November 16 that developer run scratch folders move out of the repository''s .git directory into the state directory, one for each run, so that no run''s leftover links can make a declared git server refuse every call; names the two build pieces to admit.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-16T21:34:31.331575Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 2911, for document-2911.1 under the automatic approval policy
---
# Developer run scratch folders live in the state directory, outside the repository's .git directory

**Status:** Accepted.

## Context

Each developer run is given a scratch folder, and today that folder is inside the repository's `.git` directory. The `.git` directory is shared by every worktree of the repository, so it holds what every run, finished or still going, has left in its scratch folder.

A project may declare a git server for its agents to use through the harness. The declared git server checks the whole of `.git` before it answers, and refuses every call on the repository when it finds a symbolic link there that it will not follow. A run can leave such a link behind, or a background process the run started can make one while the run is going. Three links left by the finished Codex profile run (yoyodyne-ifd.435.27) made the git server refuse every call and blocked the trial of a real server (yoyodyne-ifd.435.50).

The fix for that trial (yoyodyne-ifd.435.68) clears only the current run's own scratch folder. Links left by other runs, ended or still going, still block the server. This record decides where scratch folders live so that no run's leftovers can do that again.

## Decision

1. **Each developer run's scratch folder is in the harness's state directory, never in the repository or its `.git` directory.** The folder is `scratch/<run id>/` under the state directory: one folder for each run, named by that run's id, holding nothing of any other run.
2. **The harness creates and removes the folder through the shared write primitive, with the state directory declared as its root**, as the rule that harness writes are physically confined requires. What the developer writes inside the folder while it works is the developer's own writing, not the harness's, and is bounded by the run's sandbox.
3. **The run's sandbox lets it write to its own scratch folder and nowhere else in the state directory**, for every provider the harness ships. A run cannot write to another run's scratch folder, to run records, or to any other store in the state directory. Where a provider's sandbox cannot be narrowed to the one folder, that provider is not given the folder, and the run's start is refused with the reason, rather than given the whole state directory.
4. **The folder is removed when the run is cleaned up**, whatever way the run ended. Removal never follows a symbolic link: a link inside the folder is removed as a link, and what it points at is left untouched.
5. **Nothing a run leaves in its scratch folder is ever inside the repository**, so a declared git server's check of `.git` can no longer find it.

## Rejected

- **Keep scratch in `.git` and clear leftover links.** `.git` is shared with runs still going, so a clearing pass cannot tell a link left by an ended run from one a live run still uses without racing that run. Removing a live run's link breaks the run, and missing one leaves the server blocked. Moving the folder ends the problem rather than chasing it.
- **Change what the git server checks.** The server is the project's, declared by the project, and its check of `.git` is its own business. The harness does not rewrite a project's tools to work around where the harness itself puts things.
- **Give each run its scratch folder inside its own worktree.** A worktree's files are the change under review. Scratch there would have to be kept out of every commit and every check by name, and a mistake would put a run's scratch into a change.

## Consequences

The build pieces to admit are these:

1. **The move.** Developer runs are given `scratch/<run id>/` under the state directory instead of a folder under `.git`. The harness creates it before the run starts and removes it at clean-up, both through the shared write primitive with the state directory as root. Each provider's sandbox grants write access to that folder alone. A test shows that a symbolic link a run leaves in its scratch folder does not appear anywhere under the repository, and that the declared git server still answers after that run ends. A test for each shipped provider shows the run cannot write elsewhere in the state directory.
2. **Clearing what is already there.** One maintenance pass, run once on the first build that carries the move, removes scratch folders left under `.git` by runs that have ended. It removes links as links. It never touches a folder whose run still holds a developer slot, and it says what it removed and what it left.

The clearing of the current run's own folder from yoyodyne-ifd.435.68 does no harm after the move. It can be removed with the second piece, once no scratch folder is made under `.git`.

This is a design decision rather than a rule for the whole repository. The rule that harness writes are physically confined already governs how these folders are written. What is new here is only where they are, and a change that put scratch back under `.git` would be caught by the first piece's test.