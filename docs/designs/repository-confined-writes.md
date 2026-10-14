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
      at: 2026-10-14T02:00:27.735025Z
      reason: 'yoyodyne-ifd.437.26 and yoyodyne-ifd.437.31: the state directory is a declared root this boundary covers, ruled for the artifact kinds and state-root item; and the write itself now holds containment while a held directory is moved out of the root or a file gains a link outside it, ruled October 11 for the containment item and written here so shared confined writers (yoyodyne-xdj) can be judged against it.'
approvals:
    - policy: approvals.designs
      revision: 1
      by: harness
      at: 2026-10-14T02:00:27.735025Z
      reason: confirmed by the harness in conversation chat-a06587022b9caf04cbbb20d8f6fe8c13, turn 846, for document-846.1 under the automatic approval policy
---
# Repository-confined writes: physical containment for every owned writer

## What this is for

The August 21 security analysis confirmed escapes through symlinks in the artifact, invariant, and initialization writers. Lexical path containment, meaning prefix checks over cleaned strings, is not containment when any existing path component can be a symlink, and symlinks are legal repository content. An operation on the repository could create or change content outside it through symlink traversal, path replacement, or a missing target. This design sets the boundary that closes that class whole, serving the goal that safety invariants hold whatever the configuration says: a boundary that keeps writes inside the repository is not one a project's content should be able to opt it out of.

## The contract

One shared primitive for safe writes, a single narrowly cohesive package, through which every write the harness owns to a declared root passes. The contract's requirements, each of which the conformance suite proves rather than assumes:

- **Containment is physical, proven at the moment of the write.** The primitive resolves the actual filesystem topology and refuses a target any existing component of which resolves outside the root. String prefixes and cleaned paths prove nothing here and are not accepted as evidence.
- **Check and use are not separable.** Validation and the write must not be separated by an unbounded chance for the topology to change. The walk that validates is the walk that writes: each lookup is made relative to an open directory descriptor, component by component, refusing symlinks that leave the root. How containment holds while that directory is moved, or while a file gains another link, is set out in [Holding containment through the write](#holding-containment-through-the-write). A platform may use another mechanism only if this design is first revised to explain how it closes the same gaps; no writer decides that locally.
- **A missing descendant is accepted only under a contained ancestor.** Creation is permitted when the nearest existing ancestor physically resolves inside the root, and proceeds from that ancestor's open descriptor so the chain being created cannot be redirected while it is created.
- **A dangling symlink is not an absent file.** Writing through one creates the target wherever the link points, so it is refused, not treated as a fresh path.
- **No caller can opt out.** The package exposes no unconfined write. A writer whose legitimate target is another root declares that root; there is no flag, parameter, or fallback that waives containment for convenience.
- **Failure is explicit, attributable, and clean.** A refused operation names the operation and the reason, and leaves both the external target and the root's state unchanged: no partial write survives outside the boundary, except where [What is left open](#what-is-left-open) says otherwise.

## Roots, including the state directory

A root is the boundary a writer declares. There are three kinds: the repository, a worktree the harness manages, and the harness's state directory. All three are covered by this boundary without exception.

The state directory is not exempt because it lies outside every repository. It holds run records, conversations, memories, and locks that decide what the harness does next, and a symlink placed in it redirects a write exactly as one placed in a repository would. Every store that writes under it, including the stores in `internal/runstate` and the memory store, declares the state directory as its root and writes through the primitive. That covers creating directories, creating and replacing files, appending, creating lock files, and removing names.

Two alternatives were rejected. Exempting the state directory and saying so would leave a hole the invariant's text denies. A second, lighter primitive for state writes would make two boundaries, and two boundaries is what this design exists to prevent. The deciding constraint is that one primitive with no way to opt out is the only boundary a reviewer can check by looking for writes that bypass it.

## Holding containment through the write

### What can still go wrong after the walk

A walk relative to open descriptors closes symlink traversal, but two gaps remain after it.

- **The held directory moves.** The primitive holds an open descriptor for the directory it will write in. Something else can rename that directory, or one of its ancestors, to a place outside the root. A write through the descriptor then lands outside.
- **A file gains a link outside.** A file checked to be inside the root can acquire a hard link from a directory outside it. A later write to that file in place changes content that is visible outside.

Another check before the write alone does not close either gap. The rules below close them during the write itself.

### The root and the held directory

At the start of an operation the primitive opens the root and records its device and inode numbers. It records the name of every component it walks from the root to the held directory.

The held directory is beneath the root when both of these are true:

- walking again from the root descriptor through those recorded names, following no symlink, reaches a directory whose device and inode numbers equal the held descriptor's;
- the root path still opens to the recorded root.

The primitive checks this immediately before and immediately after every change it makes. A failed check before a change refuses the operation. A failed check after a change undoes the change as the rule for that operation says, and then refuses with an error naming the operation, the directory, and that it moved.

### Creating and replacing a file

No existing file is ever written in place, except a lock file, for which see [Lock files](#lock-files).

**Creating.** A new file is created under a fresh temporary name in the held directory, with exclusive create and no symlink followed. Before anything is written, the primitive confirms the new file is a regular file with one link, on the root's device. The content is written and synced, and the one-link check is made again. The file is then published under its real name by a rename that refuses to replace an existing name.

If the check after publication fails, the primitive removes the published name by the rule in [Removing a name](#removing-a-name).

**Replacing.** An existing file is replaced by preparing the new file the same way and then swapping the two names in one step. Swapping changes names only: the old file's content is never written. After the check, the old file, now under the temporary name, is removed by the removal rule.

If the check after the swap fails, the primitive swaps the names back, so the old content is where it was, and removes the new file.

A link that someone adds to a published file afterwards is not a write by the harness. Because later writes never go in place, that link never sees a harness write.

### Truncating

Emptying a file publishes a new empty file by the replacement rule. A file is never truncated in place.

### Appending

Appending keeps its meaning: records are added to the end of the existing file, and readers of that file see them there. This applies to conversation records and logs.

The file is opened for appending, following no symlink. It must be a regular file with one link, on the root's device; otherwise the append is refused. Records are written in batches of one whole record. Before and after each batch, the primitive checks two things:

- that the held directory is still beneath the root;
- through the open descriptor, that the file still has one link.

A failed check before a batch refuses it. A failed check after a batch stops the writer. No further batch is written, and the primitive reports that the last batch may be visible outside the root, naming the file. A conversation document that is rewritten rather than appended to follows the replacement rule.

### Lock files

A lock's identity is its file, so a lock file is opened in place. A missing lock file is created with exclusive create. An existing one is opened following no symlink. Either way it must be a regular file with one link, on the root's device, or the lock is refused.

The only content written in place is the note naming who holds the lock. That is the one write in place this primitive allows, and the harness acts on that note only to report the holder.

### Removing a name

Cleanup removes a name only while that name still refers to the file that was checked. The primitive:

1. renames the name to a fresh temporary name in the same held directory;
2. opens the temporary name, following no symlink;
3. compares its device and inode numbers with the checked file's;
4. if they match, removes the temporary name; if not, renames it back with a rename that refuses to replace, and refuses the removal.

Directories are removed the same way, depth first, through open descriptors, with the before and after checks at each level.

### Platforms

- **Linux.** Every lookup beneath the root uses `openat2` with `RESOLVE_BENEATH` and `RESOLVE_NO_MAGICLINKS`. Swaps use `renameat2` with `RENAME_EXCHANGE`. Renames that must not replace use `RENAME_NOREPLACE`. A kernel without `openat2` uses the macOS method below.
- **macOS.** Lookups walk one component at a time with `openat` and `O_NOFOLLOW`, and the primitive itself resolves any symlink it meets, refusing one that leaves the root. Swaps use `renamex_np` with `RENAME_SWAP`. Renames that must not replace use `RENAME_EXCL`. On macOS the checks before and after each change are what close the relocation gap.
- **Every other platform** refuses every write through the primitive, with an error naming the platform. There is no fallback that writes.

### What is left open

Two gaps remain, stated so nobody assumes they are closed.

- **An append can expose one record.** A link added outside the root while a single append batch is being written sees that one record. The check after the batch reports it, and the writer stops.
- **A removal can happen outside the root.** A directory moved out of the root between the last check and a removal takes the removal with it, and a removal cannot be undone. The check after the removal reports it. What was removed is the harness's own checked file, never something else.

## The conformance suite

One shared topology table, exercised against each writer rather than once in the abstract, safe to run in disposable temporary directories. It has these rows:

- a normal path;
- an existing symlinked file;
- a symlinked directory component;
- a dangling symlink;
- missing descendants under a safe ancestor;
- nested traversal;
- the held directory moved outside the root between the check and the change;
- an ancestor of the held directory moved outside the root;
- a file given a link outside the root before the write;
- a link outside the root added between two append batches;
- a lock file that has a second link;
- a name swapped for another file before a removal;
- a temporary file that would land on another device.

The races are made exact by a hook between the primitive's steps, compiled only into tests, so each row runs the same way every time.

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
- The held directory is checked against the root before and after every change, and a failed check after a change undoes it as its operation's rule says.
- No existing file is written in place except a lock file.
- New files are created with one link on the root's device and published by a rename. Replacements are published by a swap that can be reversed.
- Appends check the link count and the held directory before and after each batch, and stop on failure.
- A removal removes only the checked file.
- The state directory is a root like the others, and its stores write through the primitive.

Nothing here is left to a writer's own care or to configuration.

## The gate

The implementation gate on other V1 code lifts when, and only when, all of these hold:

- the shared contract is implemented;
- the artifact, invariant, and initialization writers use it;
- the known escape cases have regression coverage;
- the topology table passes for each of those writers.

The gate is deliberately narrow: it restores delivery while closing the confirmed defects completely, and it does not extend. Broader migration proceeds after it lifts, not inside it. Lifting the gate releases the hold on implementing code; it is not a claim that filesystem security work is complete. An independent reviewer verifies physical containment and the adversarial table before the gate lifts.

## Deferred, deliberately

- Inventory and migration of every remaining writer the harness owns to the primitive, beyond the state stores named above.
- A structural check that refuses new direct writes outside the approved package, so the boundary is enforced against future code rather than against the writers someone remembered.
- Git worktree, ref, branch publication, and remote boundary behavior, covered directly: changes to a remote are not assumed safe because ordinary file writers migrated.
- A security review stage after v1, triggered by risk and connected to management readiness rather than to a new security coordinator role.