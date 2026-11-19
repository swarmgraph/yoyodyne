---
id: shared-build-cache-outside-git
kind: decision
title: The shared Go build cache lives in the state directory, outside the repository's .git directory
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-19T05:06:45.86936Z
      reason: 'yoyodyne-ifd.435.77: records the ruling of November 18 that the shared Go build cache moves out of the repository''s .git directory into cache/go-build/ under the state directory, so the declared git server''s check of .git on every call no longer reads it; names the two build pieces to admit.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-19T05:06:45.86936Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3038, for document-3038.1 under the automatic approval policy
---
# The shared Go build cache lives in the state directory, outside the repository's .git directory

**Status:** Accepted.

## Context

The harness points the Go build cache (`GOCACHE`) of every developer run, and of every check the harness itself runs on a change, at one folder: `yoyodyne/go-build` inside the repository's own `.git` directory (`internal/execution/gocache.go`, with the folder created and granted in `internal/execution/scratch.go`). It was put there for three reasons. It is outside the working tree, so it can never leave a change's tree dirty. It is somewhere every provider's sandbox could be made to admit. And it is one folder shared by every worktree, so runs do not compile the same packages again.

A project may declare a git server for its agents to use through the harness. That server checks the whole of `.git` before it answers each call. The developer on the git server fix (yoyodyne-ifd.435.68) measured that check at about 10 seconds on November 16, against a limit of one minute for each call (report 63c47d23). A Go build cache holds many thousands of files, so it is most of what the check reads, and it grows with the cache. The Go command trims entries it has not used for some days, which bounds the growth but does not stop the cost being paid on every call.

[The decision that moved developer run scratch folders](developer-run-scratch-outside-git.md) took run scratch out of `.git` for a different reason, links the server refuses, and left the build cache where it was. This record decides where the cache lives.

## Decision

1. **The shared Go build cache is the folder `cache/go-build/` under the project's state directory, never inside the repository or its `.git` directory.** It stays one folder, shared by every developer run and every check the harness runs for the project, so nothing compiled once is compiled again for want of a shared cache.
2. **The harness creates the folder through the shared write primitive, with the state directory declared as its root**, as the rule that harness writes are physically confined requires. As today, the harness only names the folder in the environment; what the Go command writes inside it is the Go command's own writing.
3. **Each run's sandbox lets it write to that folder, and to nowhere else in the state directory beyond its own scratch folder**, for every provider the harness ships. This is the same narrowing [the scratch decision](developer-run-scratch-outside-git.md) requires: where a provider's sandbox cannot be narrowed to these folders, the run's start is refused with the reason, rather than given the whole state directory.
4. **An inherited `GOCACHE` is still replaced rather than honoured**, for the reason the code already gives: an operator's own setting is for the machine, not for a sandbox.
5. **The old folder under `.git` is removed once**, by a maintenance pass on the first build that carries the move, and only when no developer run and no check that was handed the old path is still going. It removes links as links and never follows one.

## Rejected

- **Keep the cache in `.git` and accept the time.** The check already takes a sixth of its limit, it grows with the cache, and it is paid on every call every agent makes. Nothing is gained by keeping it there that the state directory does not also give.
- **A cache for each worktree, in the worktree's own administrative folder.** That would make the git server's cost no smaller, since it still reads all of `.git`, and every run would compile every package again.
- **Leave the cache at Go's default under the user's home.** The run's sandbox refuses it there, which is the failure the redirect was written to end.
- **Change what the git server checks.** The server is the project's, and the harness does not rewrite a project's tools to work around where the harness itself puts things.

## Consequences

The build pieces to admit are these:

1. **The move.** `WithGoBuildCache` and `PrepareDeveloperDirectories` name `cache/go-build/` under the state directory instead of the folder under `.git`, for developer runs and for every check the harness runs, the merge queue's candidate checks included. Each provider's sandbox grants that folder. Tests show that after a run the repository's `.git` directory holds no build cache, that each shipped provider's run can write the cache and its own scratch folder and nowhere else in the state directory, and that two runs started one after the other reuse the first run's compiled packages. This piece rests on the sandbox narrowing the scratch move (yoyodyne-ifd.435.75) builds, so it comes after that or with it.
2. **Clearing the old folder.** The removal in decision 5. It belongs beside the one maintenance pass that clears old scratch folders under `.git` (yoyodyne-ifd.435.76), and can be the same pass if that one has not started.

One exposure is unchanged by this record and is stated so nobody reads the move as having settled it: every run can write the cache that the harness's own checks read, as it can today under `.git`. Go keys the cache by what was compiled, and the investigation of crossed results between runs (yoyodyne-ifd.238) found no crossing. A change that gave the harness's own checks a cache no run can write is a separate decision.

This is a design decision rather than a rule for the whole repository. The rule that harness writes are physically confined already governs how the folder is made; what is new is only where it is, and a change that put the cache back under `.git` would be caught by the first piece's tests.