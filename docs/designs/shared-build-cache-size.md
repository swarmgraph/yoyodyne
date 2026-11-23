---
id: shared-build-cache-size
kind: design
title: Builds share the Go build cache across worktrees, and the harness holds the shared cache under a size limit
supports:
    - operations-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-11-23T00:45:46.862248Z
      reason: 'yoyodyne-ifd.435.92: rules which part of the build cache fix belongs to this project and which to the harness. This project builds with -trimpath in its declared checks; the harness holds the shared cache folder it names under a 40 GiB default limit, trimming hourly, oldest first, never a file changed in the last two hours. Two build pieces.'
approvals:
    - policy: approvals.designs
      revision: 0
      by: harness
      at: 2026-11-23T00:45:46.862248Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3617, for document-3617.1 under the automatic approval policy
---
# Builds share the Go build cache across worktrees, and the harness holds the shared cache under a size limit

**Status:** Accepted, November 22, 2026. Serves the goal that spend follows the work: what a run costs in check time and disk is chosen for the work, not paid again for every worktree. It must not break the goals that the harness supports any language, isolates each run in its own worktree, and runs a week with no ready work waiting behind an idle slot.

## What happened

The shared Go build cache the harness names for every run (`.git/yoyodyne/go-build`, set by `WithGoBuildCache` in `internal/execution/gocache.go`) held about 760 GB in 1.8 million files on November 22, 2026. It was growing about 130 GB a day, bounded only by the Go command's own trim of entries unused for five days, and the disk was 93% used. About 590 GB unused for more than a day was deleted by hand.

The likely cause is in the Go command's compile key. Without `-trimpath`, a package's key includes the absolute folder it was built in. Every run builds in its own worktree, so this module's own packages are compiled cold and stored again in every run, and the shared cache shares only the standard library and outside modules. That costs check time on every run as well as disk.

The Makefile at e4338f8b5 sets no `GOFLAGS`. Its `dist` target already builds with `-trimpath`; nothing else does.

## Decision

### 1. This project builds with `-trimpath` in its declared checks

How a project's code is compiled is the project's choice, so this half belongs to this repository's declared checks, not to the harness.

- The Makefile exports `GOFLAGS` carrying `-trimpath` for every target that runs a Go command: `build`, `test`, `race`, `vet`, `test-macos`, `doccheck`, `codex-resume`, and the scripts those targets call, such as `scripts/race-run.sh` and `scripts/rerun-failing.sh`. It adds the flag to any `GOFLAGS` already set rather than replacing it.
- The project's instructions to developers say to run Go commands through make, or with `-trimpath` when running one directly, so a developer's own probe compiles against the same entries as the checks.
- Any test that finds a file from its own source path, for example by the path `runtime.Caller` returns, is changed to find it from the test's working folder, which Go sets to the package folder. With `-trimpath` such paths are module paths, not absolute ones.

### 2. The harness holds the shared cache folder under a size limit

The harness creates and names the shared cache folder, so keeping that folder bounded is the harness's work. It does this without knowing anything about Go: it trims files in a folder it owns.

- **The limit.** A project setting under `execution`, which the build piece names, gives the limit in GiB. The default is 40. Loading refuses a value under 5 or over 1000, and the bounds are held in Go code.
- **When it runs.** At least once an hour: in the harness's recurring maintenance pass if that runs hourly or more often, otherwise on an hourly turn of its own in the scheduler.
- **What it removes.** When the folder is over the limit, it removes files oldest last change first until the folder is at 80% of the limit, so it does not trim on every pass. It never removes a file changed in the last two hours, because the Go command refreshes the time of an entry it uses at most once an hour, and a build that is running may be reading it. It removes only regular files, leaves folders and the Go command's own bookkeeping files in place, removes a link as a link and never follows one, and removes through the shared write primitive with the cache folder's parent declared as the root.
- **Which folder.** Whatever folder the harness names as the shared build cache, read from the same function `WithGoBuildCache` uses. Today that is under `.git`; after the move into the state directory (yoyodyne-ifd.435.78) it is `cache/go-build/` there. The limit does not wait for the move.
- **What anyone can see.** Each trim records the size before and after, the number of files removed, and the time, in the record of the pass that ran it. If the folder is still over the limit after a trim, because everything in it was changed in the last two hours, the harness files one warning report to the development manager naming the size and the limit, at most once a day.
- **On a restart.** Nothing is held between trims, so a trim cut off by a restart is simply done again by the next one.

## Rejected

- **The harness setting `-trimpath` for every run.** The harness sets `GOCACHE` only because a run's sandbox refuses Go's default folder; that is a sandbox matter. Changing how a project compiles its code is a build choice, it can break a project's tests, and the harness assumes no language.
- **Relying on the Go command's own five-day trim.** That is what let the cache reach 760 GB.
- **Clearing the whole cache on a timer.** Every run after it compiles everything cold, and a build that is running can lose files it is reading.
- **Building every run at one fixed folder so the paths match.** Runs build side by side in separate worktrees; one folder cannot serve them.

## Build pieces

1. **Builds with `-trimpath` (this project).** The Makefile change and the instruction line in section 1, and the tests that read absolute source paths changed to read from their working folder. Done when `make test` passes, and a measured run, recorded on the item, shows two worktrees at the same commit sharing one cache: the second `go build -x ./...` runs no compile command for this module's packages.
2. **The size limit (harness).** Section 2: the setting with its bounds, the hourly trim, the record and the warning. Tests show that a folder over the limit is trimmed oldest first to 80% of it; that a file changed in the last two hours is never removed; that a link is removed as a link and its target is untouched; that a setting outside the bounds is refused at load; and that the trim reads the folder from the same function the run's environment uses.

The two pieces are independent and can land in either order. The first ends most of the growth; the second keeps the folder bounded whatever writes it.
