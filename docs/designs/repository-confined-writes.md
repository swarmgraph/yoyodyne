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
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T14:13:52.801041Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 938, for document-938.1 under the automatic approval policy
---
# Repository-confined writes: physical containment for every owned writer

## What this is for

The August 21 security analysis confirmed that the artifact, invariant, and initialization writers could be led outside the repository. Checking a path as a string is not containment when any existing part of the path can be a symbolic link, and symbolic links are legal repository content. A write made for the repository could create or change content outside it by following a link, by a path being replaced, or by a missing file being treated as safe to create.

A later review of the preserved implementation found two more ways out that a check made before writing cannot close:

- a directory the writer holds open can be moved outside the root after it was checked, so the write lands outside;
- a file can gain a second link from outside the root after it was checked, so writing to it changes a file outside.

This design is the one boundary that closes the whole class. It serves the goal that safety invariants hold whatever the configuration says: keeping writes inside the repository is not something a project's content should be able to switch off.

It is the design the recovery of the shared confined writers (yoyodyne-xdj) is built against. Its revision for containment against directory moves and late links is yoyodyne-ifd.437.31. The state directory's part of it comes from the artifact kinds and state root ruling (yoyodyne-ifd.437.26).

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
- **A refusal says what was refused and why, and leaves nothing behind.** It names the operation, the root, the path relative to the root, and one reason from the fixed list below. Neither the root nor anything outside it is left changed: a change the after check rejects is undone.

## Looking a path up

Every lookup starts from the root's descriptor. Each platform uses its own method, and every other platform is refused.

- **Linux, kernel 5.6 or later.** `openat2` with `RESOLVE_BENEATH`, `RESOLVE_NO_MAGICLINKS` and `RESOLVE_NO_XDEV`. The kernel refuses a lookup that would leave the root, and refuses one that would cross onto another mounted filesystem.
- **macOS 11 or later.** The walk opens each part with `openat` and `O_NOFOLLOW`, relative to the directory held before it. Where a part is a symbolic link, the walk reads it with `readlinkat` and resolves its target by the same walk from the directories already held. It refuses a target that is absolute, a `..` that would climb above the root, and a part on another device than the root. At most 40 links are followed in one lookup.

On both platforms, a symbolic link inside the root whose relative target stays inside the root is followed as before, so contained paths keep working.

A symbolic link written as an absolute path is refused even when it points inside the root. This narrows what the August text allowed, and it is deliberate: an absolute path names a place by string, and a string proves nothing about where it lands.

The primitive records, for each directory it holds:

- the path relative to the root by which it was opened;
- its device and file number, taken with `fstat` on the held descriptor.

## Checking a held directory

The check looks the recorded relative path up again from the root's descriptor, by the method above. It then compares the device and file number found with those of the held descriptor. The held directory is still where it was, beneath the root, only if they match.

The check runs before every change and again after it. A directory moved out and back between the two checks leaves the change inside the root, which is where it rests.

## Each kind of change

New files are written under a name that begins `.yoyodyne-tmp-` and is unique for each write, in the target's own directory. They are created with `O_CREAT`, `O_EXCL` and `O_NOFOLLOW`, written, synced, and checked to have exactly one link before they are published.

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

These are the filesystems that provide both the exchange and the rename that refuses to replace. On any other platform or filesystem the primitive refuses every write and names what it found. There is no fallback to an ordinary rename or an ordinary write, because neither keeps the guarantee.

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

The harness's state directory is a root like the others. Every store in `internal/runstate`, the memory store, the conversation records and the lock files write through the primitive with the state directory declared as their root. Nothing written there is excused because the harness, rather than the project, owns the directory.

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
- a second link from outside added to a file being replaced or shortened;
- the file at a removal target swapped for another between the check and the removal;
- a lock file with a second link;
- an unsupported filesystem.

The moves and links happen at exact points through test hooks compiled only into tests, so each row runs the same way every time.

The writers each row runs against are:

- artifact, invariant, and initialization writers;
- document publication;
- the stores in `internal/runstate` and the memory store;
- conversation records;
- lock files;
- cleanup.

Each writer produces the same safe result, or the same kind of error, for each row. The minimal reproductions of the confirmed escapes are kept as regression tests, separate from the table, so the specific defects that forced this design cannot silently return.

## What is enforced in code

- Every lookup is relative to an open descriptor beneath the root, by the platform's method above, and every other platform is refused.
- The held directory is checked against the root before and after every change, and a failed check after a change undoes it as that change's rule says.
- No existing file is written in place except a lock file.
- New files are created with one link on the root's device and published by a rename. Replacements are published by a swap that can be reversed.
- Appends check the link count and the held directory before and after each batch, and stop on failure.
- A removal removes only the checked file.
- The state directory is a root like the others, and its stores write through the primitive.

Nothing here is left to a writer's own care or to configuration.

## Alternatives rejected

- **A second check before writing.** It narrows the window between check and change and leaves it open. The item that asked for this revision says so, and the moved directory row proves it.
- **Locking the directory against change.** Advisory locks bind only programs that ask for them, and nothing makes another process ask.
- **Writing every file in place after checking its link count.** A link added between the check and the write carries the write outside. Publishing a new file is the only way a later link cannot change what was outside.
- **Refusing every symbolic link.** That would break contained paths that repositories legitimately use. Following only relative links that stay inside keeps them working.
- **A fallback to ordinary writes on other filesystems.** It would silently give up the guarantee wherever the swap is missing. Refusing with the filesystem named is honest, and the supported list can grow by revising this design.

## The gate

The implementation gate on other V1 code lifts when, and only when, all of these hold:

- the shared contract is implemented;
- the artifact, invariant, and initialization writers use it;
- the known escape cases have regression coverage;
- the table passes for each of those writers, including the rows for moved directories and added links.

The gate is deliberately narrow. It restores delivery while closing the confirmed defects completely, and it does not extend: broader migration proceeds after it lifts, not inside it. Lifting the gate releases the hold on implementing code; it is not a claim that filesystem security work is complete. An independent reviewer verifies physical containment and the table before the gate lifts.

## Deferred, deliberately

- Inventory and migration to the primitive of every remaining writer the harness owns, beyond the state stores named above.
- A structural check that refuses new direct writes outside the approved package, so the boundary is enforced against future code rather than against the writers someone remembered.
- Git worktree, ref, branch publication, and remote boundary behavior, covered directly: changes to a remote are not assumed safe because ordinary file writers migrated.
- A security review stage after v1, triggered by risk and connected to management readiness rather than to a new security coordinator role.
