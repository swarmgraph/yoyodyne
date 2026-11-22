---
id: repository-confined-writes
kind: design
title: 'Repository-confined writes: physical containment for every owned writer'
supports:
    - v1-goals
status: active
revisions:
    - action: created
      by: architect
      at: 2026-08-23T04:09:30Z
      reason: promoted from the operator's V1 scope-reconciliation brief on repository-confined writes, under the 2026-08-22 mandate; deviations from the brief recorded in the promotion decision
    - action: amended
      by: architect
      at: 2026-10-14T14:13:52.801041Z
      reason: Rewritten whole for containment against directory moves and late hard links, because the earlier copy kept by the harness could not be sent again from this conversation. Adds the checks of the held directory on both sides of every change, publishing new files rather than writing existing ones in place, the supported platforms with a fixed list of refusal reasons, the state directory as a root, and table rows for moved directories and added links across every writer. The invariant's matching additions wait until invariants can be sent from a conversation (yoyodyne-0bj).
    - action: amended
      by: architect
      at: 2026-10-20T13:01:21.273322Z
      reason: 'Makes two changes the developer on the confined writers rebuild (yoyodyne-xdj) proposed and the architect approved on 20 October: a child process''s log is fed through a pipe the harness appends from with the batch checks, never through a descriptor handed to the child (amendment-cd567551); test hooks are compiled into the package but refuse to be set outside a test binary (amendment-7a92c2cf). The rest of the text is unchanged.'
    - action: amended
      by: architect
      at: 2026-11-19T17:41:30.561837Z
      reason: Replaces the retired word 'gate' under the operator's November 8 rule, as part of replacing the retired words in the governed documents (yoyodyne-ifd.437.21). The section 'The gate' is renamed 'When other v1 code may be built', and its five other uses become 'hold' and 'lifted'. Its meaning is unchanged, and nothing else in the document changes. Written from main 1879aa6c6.
    - action: amended
      by: architect
      at: 2026-11-22T06:04:54.212799Z
      reason: 'Brings the design into line with the code at main 62d9a16a5, from the second comparison reading of the checks designs. Adds what is built and what is not: most state stores write with plain file calls, the Slack log descriptor goes to its child, the writer cuts files in place, and none of the descriptor walk or link checks exist. The standing rule stays. Adds an eight step build order (pipe, replacement, a shrinking list of direct writers in make test, the stores, locks, then strengthening) and how review holds the rule meanwhile.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T14:13:52.801041Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 938, for document-938.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 2
      by: harness
      at: 2026-10-20T13:01:21.273322Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 1344, for document-1344.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 3
      by: harness
      at: 2026-11-19T17:41:30.561837Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3120, for document-3120.1 under the automatic approval policy
    - policy: approvals.designs
      revision: 4
      by: harness
      at: 2026-11-22T06:04:54.212799Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 3410, for document-3410.1 under the automatic approval policy
---
# Repository-confined writes: physical containment for every owned writer

## What this is for

The August 21 security analysis confirmed that the artifact, invariant, and initialization writers could be led outside the repository. Checking a path as a string is not containment when any existing part of the path can be a symbolic link, and symbolic links are legal repository content. A write made for the repository could create or change content outside it by following a link, by a path being replaced, or by a missing file being treated as safe to create.

A later review of the preserved implementation found two more ways out that a check made before writing cannot close:

- a directory the writer holds open can be moved outside the root after it was checked, so the write lands outside;
- a file can gain a second link from outside the root after it was checked, so writing to it changes a file outside.

This design is the one boundary that closes the whole class. It serves the goal that safety invariants hold whatever the configuration says: keeping writes inside the repository is not something a project's content should be able to switch off. The standing rule it carries is [repository-writes-are-physically-confined](../decisions/invariants/repository-writes-are-physically-confined.md).

It is the design the recovery of the shared confined writers (yoyodyne-xdj) is built against. Its revision for containment against directory moves and late links is yoyodyne-ifd.437.31. The state directory's part of it comes from the artifact kinds and state root ruling (yoyodyne-ifd.437.26).

## Where this stands

Read against the code at main 62d9a16a5 (November 21, 2026), from the second comparison reading of the checks designs against the code under docs/diagnoses. Most of this design is a plan, not yet built, and the standing rule is not yet met. This section says exactly what is built; the sections after it say what must be true when the work is finished, and [Build order](#build-order) says how the gap is closed.

Built:

- A confined writer package, internal/repowrite, with two strengths. Root checks a path by resolving it, then writes by path; the package's own header says this does not hold if the path is swapped between the check and the write. PinnedRoot uses Go's os.Root handles, which keep lookups beneath the root.
- The artifact, invariant and initialization writers, document publication, the install cache, outside file landing, pinned files, the heavy check permits, the merge queue records, launch records, the dashboard, follow ups, tracker exports and locks, the MCP call record and the intake record write through one of the two.
- A shared table of seven fixed filesystem shapes, run against the artifact, invariant, initialization, worktree, Slack and two run state writers.
- Logs of MCP servers come through pipes the harness reads.

Not built:

- The descriptor walk with openat2 or openat, the checks of the held directory before and after each change, the exchange and no replace renames, link counts, batched appends with checks, the removal holding directory, the platform and filesystem list, and the test hooks.
- Refusing a symbolic link given as an absolute path: Root follows any link whose target resolves inside the root, absolute ones included. PinnedRoot refuses every symbolically linked directory, which is stricter than this design asks and is allowed.
- The state directory as a root written only through the confined writer. In internal/runstate, 56 files that are not tests write with plain file calls, and 51 of them never use the confined writer. Among them are the run record store, the conversation records, the intake hold and the reservation lock. Slack's state and several worktree and setup files do the same, and the memory store takes its lock and renames its archive directly.
- A child's log through a pipe: the Slack service opens its log through the confined writer and then hands the descriptor to the program it starts.
- Never shortening a file in place: Root.Truncate cuts a file in place, and the memory store uses it.

## The contract

One shared write primitive, in a single small package, through which every write the harness owns passes. A root is the directory a writer declares:

- the repository;
- a harness-managed worktree;
- the harness's state directory.

The root is opened once and held by an open descriptor for the whole operation. The conformance suite proves each requirement below rather than assuming it.

- **Containment is physical, and proven when the change is made.** The primitive resolves the real filesystem and refuses any target with an existing part that resolves outside the root. A string prefix or a cleaned path proves nothing here and is not accepted as evidence.
- **Checking and changing are not separate steps.** The walk that checks is the walk that writes, relative to open descriptors beneath the root, one path part at a time.
- **A held directory is proven still beneath the root on both sides of every change.** A check made only before writing is not enough.
- **No existing file is changed in place, except a lock file.** Content is published as a new file, so a link that appeared later can never carry a write outside.
- **A missing descendant is accepted only under a contained ancestor.** Creation proceeds from the nearest existing ancestor that is physically inside the root, relative to its open descriptor, so the chain being created cannot be redirected while it is created.
- **A symbolic link is never written through.** A symbolic link as the last part of a write's target is refused, whether or not what it points at exists.
- **No caller can opt out.** The package offers no unconfined write. A writer whose real target is another root declares that root. No flag, parameter, or fallback waives containment.
- **No descriptor the primitive opened is handed to another process.** Every write to a file the primitive opened is made by the primitive, so every check below runs for it.
- **A refusal says what was refused and why, and leaves nothing behind.** It names the operation, the root, the path relative to the root, and one reason from the fixed list below. Neither the root nor anything outside it is left changed: a change the after check rejects is undone.

## Looking a path up

Every lookup starts from the root's descriptor. Each platform uses its own method, and every other platform is refused.

- **Linux, kernel 5.6 or later.** `openat2` with `RESOLVE_BENEATH`, `RESOLVE_NO_MAGICLINKS` and `RESOLVE_NO_XDEV`. The kernel refuses a lookup that would leave the root, and refuses one that would cross onto another mounted filesystem.
- **macOS 11 or later.** The walk opens each part with `openat` and `O_NOFOLLOW`, relative to the directory held before it. Where a part is a symbolic link, the walk reads it with `readlinkat` and resolves its target by the same walk from the directories already held. It refuses a target that is absolute, a `..` that would climb above the root, and a part on another device than the root. At most 40 links are followed in one lookup.

On both platforms, a symbolic link inside the root whose relative target stays inside the root is followed as before, so contained paths keep working.

A symbolic link written as an absolute path is refused even when it points inside the root. This narrows what the August text allowed, and it is deliberate: an absolute path names a place by string, and a string proves nothing about where it lands. The built Root does not yet refuse it; see [Where this stands](#where-this-stands).

The primitive records, for each directory it holds:

- the path relative to the root by which it was opened;
- its device and file number, taken with `fstat` on the held descriptor.

## Checking a held directory

The check looks the recorded relative path up again from the root's descriptor, by the method above. It then compares the device and file number found with those of the held descriptor. The held directory is still where it was, beneath the root, only if they match.

The check runs before every change and again after it. A directory moved out and back between the two checks leaves the change inside the root, which is where it rests.

## Each kind of change

New files are written under a name that begins `.yoyodyne-tmp-` and is unique for each write, in the target's own directory. They are created with `O_CREAT`, `O_EXCL` and `O_NOFOLLOW`, written, synced, and checked to have exactly one link before they are published. The built package names them `.yoyo-write-` followed by a unique part; the name is not part of the guarantee, and the build that strengthens the primitive may keep either.

Creating a file:

1. Check the held directory.
2. Write the new file under a temporary name.
3. Publish it under its real name by a rename that refuses to replace anything: `renameat2` with `RENAME_NOREPLACE` on Linux, `renameatx_np` with `RENAME_EXCL` on macOS.
4. Check the held directory again.
5. If that check fails, remove the published name, after confirming it is still the file just written by its device and file number. That returns the directory, wherever it now is, to how it was.

Replacing a file:

1. Check the held directory.
2. Write the new content under a temporary name.
3. Swap it with the existing file: `renameat2` with `RENAME_EXCHANGE` on Linux, `renameatx_np` with `RENAME_SWAP` on macOS.
4. Check the held directory again.
5. If that check fails, swap back.
6. In either case, remove the temporary name, which now holds the old content.

The old file's content is never changed, so a link to it from outside keeps exactly what it had.

Shortening a file is a replacement whose new content is the part kept. A file is never truncated in place.

Appending:

1. Open the file through the walk with `O_WRONLY`, `O_APPEND` and `O_NOFOLLOW`.
2. Before each batch of at most 64 KiB, check the held directory and confirm with `fstat` that the file has exactly one link.
3. After each batch, check both again.
4. If either fails, stop, refuse every further append to that file in this operation, and report it.

What appending means to a reader is unchanged.

The harness cannot stop another program running as the same user from linking a file it can see. What it guarantees is narrower and exact:

- it never writes to a file that already had a second link when checked;
- it stops within one batch of a link appearing;
- it never changes any file that was outside the root.

The batch already written when a link appeared is not undone, because undoing it would be another write through the same link.

### Output of a child process

A child process the harness starts, such as the Slack service, may have its output kept in a log. The child is never given a descriptor for that log, because a child writing to it directly would skip the checks above. Instead:

1. The child's output goes to a pipe. The child holds the pipe's writing end and nothing that names the log file.
2. The harness process that started the child reads the pipe and appends what it reads to the log through the primitive, in batches of at most 64 KiB, with the checks before and after each batch.
3. If the checks stop the appends, the harness keeps reading the pipe and discards what it reads, so the child never blocks on a full pipe. It reports the stop once, naming the log and the reason.
4. If the harness process copying the pipe ends, the child's further output is lost rather than written unchecked.

Output that a service manager such as launchd writes for a service it runs is the service manager's write, not the harness's, and this design does not cover it; see the deferred list.

Removing a file:

1. Rename the checked name, without replacing anything, into the primitive's own holding directory, `.yoyodyne-removing` at the root.
2. Confirm that what arrived there is the file that was checked, by its device and file number.
3. If it is, remove it there. If it is not, rename it back and refuse.

A removal removes only the file that was checked.

Creating a directory:

1. Check the held directory.
2. Create the new directory with `mkdirat`.
3. Open it with `O_NOFOLLOW` and `O_DIRECTORY`, and record it.
4. Check the held directory again.
5. If that check fails, remove the new directory.

Lock files are the one exception to publishing new files. A lock is identified by the file itself, so it is created once with `O_EXCL` and never replaced, which keeps lock identity stable. Before and after every write to a lock file, the primitive checks the held directory and confirms the file has exactly one link. A lock file found with more than one link is refused and not written.

## Supported environments, and what is refused

Writes are supported on two platforms:

- Linux 5.6 or later, on ext4, XFS, Btrfs or tmpfs;
- macOS 11 or later, on APFS.

These are the filesystems that provide both the exchange and the rename that refuses to replace. On any other platform or filesystem the primitive refuses every write and names what it found. There is no fallback to an ordinary rename or an ordinary write, because neither keeps the guarantee. Today PinnedRoot refuses only two platforms Go names js and plan9, Root checks no platform, and nothing checks the kernel version or the filesystem.

Every refusal gives one reason from this list:

- a symbolic link leading outside the root;
- a symbolic link given as an absolute path;
- a symbolic link as the last part of the target;
- a path crossing onto another filesystem;
- a held directory no longer beneath the root;
- a file with more than one link;
- a removal target that changed before it was removed;
- an unsupported platform;
- an unsupported filesystem.

## The state directory

The harness's state directory is a root like the others. Every store in `internal/runstate`, the memory store, the conversation records, Slack's state and the lock files write through the primitive with the state directory declared as their root. Nothing written there is excused because the harness, rather than the project, owns the directory.

Most of those stores do not yet do so; [Build order](#build-order) moves them. Until the last of them has moved, the standing rule is held this way:

- A change that adds a direct write under any of the three roots, outside the confined writer's package, is refused by review and by the check in step 3 below.
- A change that touches a store still on that check's list, without adding a direct write, is not refused for the writes already there. Moving that store is its own step.

## Build order

Each step is one bounded piece of work, verified on its own, in this order:

1. **The Slack service's log goes through a pipe.** The program the Slack service starts is given a pipe; the harness process that started it copies the pipe to the log through the confined writer, as [Output of a child process](#output-of-a-child-process) says. The writer table's row for a child's log runs against it.
2. **Shortening a file becomes a replacement.** Root.Truncate is replaced by a write of the kept part as a new file published over the old one, and the memory store uses it. No call that cuts a file in place is left in the package.
3. **A check in make test lists every direct writer.** It lists each file that is not a test and calls a direct file write (create, rename, open for writing, make directories, remove, truncate) under internal/runstate, internal/slack, internal/gitworktree, internal/cli and internal/home, outside internal/repowrite. The list is kept in the repository and may only shrink: a file added to it fails the check, and a file that no longer writes directly must be taken off it. This is the same kind of check the design for one owner for each guarantee uses.
4. **The run record store writes through the confined writer,** including its reservation lock and its appends, and leaves the list.
5. **The conversation records write through the confined writer,** and leave the list.
6. **The rest of the state directory moves,** in reviewable groups: the intake hold and Slack's state first, then the remaining run state stores a few files at a time, each group leaving the list.
7. **Lock files open through the confined writer's lock call,** including those in the run state stores that already use it for everything else and the worktree lease and registry.
8. **The confined writer is strengthened to the whole contract:** the descriptor walk on both platforms, the checks of the held directory before and after each change, the exchange and no replace renames, link counts, batched appends with checks, the removal holding directory, refusing an absolute symbolic link, the platform and filesystem list, the conformance rows for moved directories and added links, and the test hooks. This step may be split by kind of change. When it lands, every writer on the moved list is covered at once.

The order puts the strengthening last on purpose. Once every writer uses one package, making that package meet the contract is one change in one place. Strengthening it first would leave half the writers outside it while the rest moved onto a package that was itself still changing.

## The conformance suite

One shared table of filesystem shapes, run against each writer rather than once in the abstract, in disposable temporary directories. Its rows are:

- a normal path;
- an existing symbolic link to a file;
- a symbolic link as a directory part;
- a symbolic link that points at nothing;
- a symbolic link given as an absolute path, pointing inside the root;
- missing descendants under a safe ancestor;
- nested traversal;
- a path crossing onto another filesystem;
- a held directory moved outside the root between the first check and the change;
- a held directory moved outside the root between the change and the second check;
- an ancestor of the held directory moved outside the root;
- a second link from outside added to a new file before it is published;
- a second link from outside added to a file between two append batches;
- a second link from outside added to a child process's log between two batches copied from its pipe;
- a second link from outside added to a file being replaced or shortened;
- the file at a removal target swapped for another between the check and the removal;
- a lock file with a second link;
- an unsupported filesystem.

The built table, in internal/repowrite/writertest, has the first seven of these as fixed shapes. For a link to elsewhere in the root it accepts either following or refusing; once step 8 lands it requires refusal for an absolute link.

The moves and links happen at exact points through test hooks, so each row runs the same way every time. The hooks are compiled into the primitive's package rather than only into its tests, because the tests of other writers, such as the conversation store's, drive the same hooks and Go cannot reach a hook in another package unless a production file exports it. Three things keep that safe:

- The function that sets a hook calls `testing.Testing()` and panics when it reports that the program is not a test binary, so no production code path can install a hook.
- With no hook set, each hook point is one check that the hook is unset, and changes nothing.
- A test builds and runs a small program that is not a test binary, tries to set a hook, and confirms the panic.

The writers each row runs against are:

- artifact, invariant, and initialization writers;
- document publication;
- the stores in `internal/runstate` and the memory store;
- conversation records;
- Slack's state;
- lock files;
- logs of child processes the harness starts;
- cleanup.

Each writer produces the same safe result, or the same kind of error, for each row. The minimal reproductions of the confirmed escapes are kept as regression tests, separate from the table, so the specific defects that forced this design cannot silently return.

## What is enforced in code

When the build order is finished:

- Every lookup is relative to an open descriptor beneath the root, by the platform's method above, and every other platform is refused.
- The held directory is checked against the root before and after every change, and a failed check after a change undoes it as that change's rule says.
- No existing file is written in place except a lock file.
- New files are created with one link on the root's device and published by a rename. Replacements are published by a swap that can be reversed.
- Appends check the link count and the held directory before and after each batch, and stop on failure.
- No descriptor the primitive opened is handed to another process; a child's log is written by the harness from a pipe.
- A removal removes only the checked file.
- The state directory is a root like the others, and its stores write through the primitive.
- A check in make test refuses any new direct write outside the primitive's package.
- A test hook cannot be set outside a test binary.

Nothing here is left to a writer's own care or to configuration.

## Alternatives rejected

- **A second check before writing.** It narrows the window between check and change and leaves it open. The item that asked for this revision says so, and the moved directory row proves it.
- **Locking the directory against change.** Advisory locks bind only programs that ask for them, and nothing makes another process ask.
- **Writing every file in place after checking its link count.** A link added between the check and the write carries the write outside. Publishing a new file is the only way a later link cannot change what was outside.
- **Refusing every symbolic link.** That would break contained paths that repositories legitimately use. Following only relative links that stay inside keeps them working.
- **A fallback to ordinary writes on other filesystems.** It would silently give up the guarantee wherever the swap is missing. Refusing with the filesystem named is honest, and the supported list can grow by revising this design.
- **Handing a child its log's descriptor, checked only when opened.** A link added after the opening would carry every later write by the child outside the root, which is exactly the case the append checks exist for.
- **Test hooks compiled only into tests by a build tag.** A test run without the tag would skip the rows for moved directories and added links without saying so, and a skipped row reads as a pass.
- **Narrowing the standing rule to the writers that already comply.** It would make the rule true by giving up the boundary the brief asks for. The rule stays, and the build order closes the gap.
- **Strengthening the primitive before moving the writers.** Rejected for the reason given under [Build order](#build-order).

## When other v1 code may be built

Implementing other V1 code was held until, and only until, all of these hold:

- the shared contract is implemented;
- the artifact, invariant, and initialization writers use it;
- the known escape cases have regression coverage;
- the table passes for each of those writers, including the rows for moved directories and added links.

The hold is deliberately narrow. It restores delivery while closing the confirmed defects completely, and it does not extend: broader migration proceeds after it is lifted, not inside it. Lifting the hold releases implementing code; it is not a claim that filesystem security work is complete. An independent reviewer verifies physical containment and the table before the hold is lifted.

The hold was lifted with the artifact, invariant and initialization writers on Root and the seven fixed shapes passing. The rows for moved directories and added links did not exist then and do not exist now, so the last condition was not met as written; step 8 of the build order is what meets it.

## Deferred, deliberately

- Output a service manager such as launchd writes for a service the harness defines: that is the service manager's write, and checking the path the harness gives it is a later revision of this design.
- Git worktree, ref, branch publication, and remote boundary behavior, covered directly: changes to a remote are not assumed safe because ordinary file writers migrated.
- A security review stage after v1, triggered by risk and connected to management readiness rather than to a new security coordinator role.
