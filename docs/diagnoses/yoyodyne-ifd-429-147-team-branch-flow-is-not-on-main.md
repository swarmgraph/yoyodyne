# yoyodyne-ifd.429.147: the team-branch flow this check walks is not on main yet

The end-to-end check of integrating to a local branch (yoyodyne-ifd.429.147) is
piece 6 of [the design](../designs/integrating-to-a-local-branch.md). It asks
for one check against a scratch repository that takes an item from start,
through the team's pull request, feedback, a moved target and the team's
merge, to closed, and fails if any step does not happen. The item itself says
it runs after pieces 2 to 5.

On this base only piece 1 is on main. Pieces 2, 4 and 5 are written and kept on
branches that have not merged, and piece 3 has no code anywhere. A check that
walks the whole flow would have nothing to drive past the first step, so this
run built none and records why.

## What is and is not here

Run in this worktree on 2026-11-10 at base commit `737c64da`:

```sh
# Piece 1, the team's branch setting and the wait for the team's merge: present.
grep -rln 'UpstreamBranch' --include='*.go' internal cmd | wc -l     # 19 files
ls internal/orchestrator/teammerge*.go                               # five files

# Piece 2, `yoyo pull-request track`: absent. Every match is an unrelated
# policy name or follow-up subject.
grep -rnE '"pull-request"|TrackedPullRequest' --include='*.go' internal cmd

# Piece 4, bringing the target branch up to date with the team's branch: absent.
grep -rnE 'UpstreamUpdate|upstreamUpdate' --include='*.go' internal cmd | wc -l   # 0

# Piece 5, `yoyo landing hold`: absent. Every match is another kind of hold.
grep -rnE 'LandingHold|landingHold' --include='*.go' internal cmd

# Where each piece's work is kept, and whether main holds it.
for b in 144/6984adf7 145/a31cb306 146/8e4b340e 130/26bcb2f0; do
  git merge-base --is-ancestor yoyodyne/yoyodyne-ifd-429-$b HEAD \
    && echo "$b on main" || echo "$b not on main"
done
```

| Piece | Work item | Where it stands on 2026-11-10 |
| --- | --- | --- |
| 2. tracking the team's pull request | yoyodyne-ifd.429.144 | approved, waiting in the merge queue (pull request #1729, commit `b2b41e5c`) |
| 3. feedback on a tracked pull request | yoyodyne-ifd.429.145 | no code; its run found it needs piece 2 on main and landed a plan (pull request #1733) |
| 4. bringing the target branch up to date | yoyodyne-ifd.429.146 | refused at review and waiting on a narrower item; the conflict-resolving run was split out as yoyodyne-ifd.429.146.1 |
| 5. the landing hold | yoyodyne-ifd.429.130 | approved, waiting in the merge queue (pull request #1706, commit `4e79cb76`) |

## Why a partial check is not landed instead

A check covering piece 1 alone would not meet any of the item's three
conditions: the first asks for the whole flow and for failure when any step
is missing. Piece 1's own tests already cover what it adds
(`internal/orchestrator/teammerge_test.go`,
`internal/orchestrator/publicationtests/teammerge_test.go`). Writing the
scaffolding now against interfaces pieces 2 to 5 have not settled on main would
be rewritten when they land.

## What releases the item

Pieces 2, 3, 4 (with its conflict-resolving run, 4b) and 5 closed, with their
changes on main. Then the check can be built as the design's piece 6 says,
against every line under *What a reviewer can check*, and its result recorded
beside the other end-to-end checks in [working on yoyo](../developing-yoyo.md).
