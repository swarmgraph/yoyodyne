# Release notes

*One file per release tag, named for it. Part of
[yoyo's documentation](../../README.md#further-reading).*

`v0.3.1.md` is what changed in `v0.3.1`. The file is written before the tag
exists and committed with the commit the tag names, so the release page and the
repository tell one story rather than two: the
[release workflow](../../.github/workflows/release.yml) publishes this file as
the release's body, with the install preamble under it and nothing else — the
forge's own commit-derived changelog is never appended, because a commit says
what one change did and these notes say what somebody wanted. The forge
accepts a body of 125,000 characters, and
[what a release page carries](../developing-yoyo.md#what-a-release-page-carries)
says where a file approaching that is warned about and where one over it is
refused.

[`v0.3.0.md`](v0.3.0.md) is the exception and says so here rather than in
itself: it was written after `v0.3.0` was tagged and pushed, so the repository
carries it but that release's published page does not. Every tag from `v0.3.1`
on has its notes before it exists.

## The shape

Three sections, in this order, because it is the order a reader needs them in:

1. **Key functionality** — what the release is *for*. A newcomer reading only
   this heading should be able to say what changed.
2. **Enhancements** — what got better about something that already worked.
3. **Bug fixes** — what was broken and is not any more.

There is one exception, and only one: **a critical bug fix may go up with key
functionality**. A release whose headline is that a data-loss defect is closed
should say so at the top, not three headings down.

## How a file gets here

[`scripts/release-notes.sh`](../../scripts/release-notes.sh) drafts one from
what actually landed — the work items closed between the previous release tag
and this one, read from the tracker's export, `.beads/issues.jsonl`, with their
titles, their descriptions, their types, and the goals they served. The work
item behind a change says what somebody wanted, which is the difference between
notes and a changelog:

```sh
make release-notes VERSION=v0.3.1               # draft docs/releases/v0.3.1.md
bash scripts/release-notes.sh v0.3.1 --print    # see it without writing it
```

Every entry has one shape: the title and the id on the first line, the goal
the item serves under it, then — only for an item that did not originate with
a person — one line saying which persona asked for it and why, and then the
item's description as its own paragraphs.

```markdown
- **The title** (`yoyodyne-ifd.404`)
  Serves: the goal the item names
  Requested by the reviewer: the reason, in the reviewer's own words

  The item's description, paragraph by paragraph.
```

Which items get the `Requested by` line is read off the admission the item's
notes record: one admitted from a role's collected report names that role, in
the report's own words, and one the development manager decomposed out of a
parent names the development manager, the parent, and the reason recorded. An
item the operator asked for through the Lead Product Manager carries no such line.
The three releases here today — [`v0.3.0.md`](v0.3.0.md),
[`v0.4.0.md`](v0.4.0.md), and [`v0.5.0.md`](v0.5.0.md) — were drafted before
this shape existed and carry titles and goals only; the next draft is the first
written this way. [Redrafting one of those three](../developing-yoyo.md#every-cut-writes-its-notes)
is a person's commit rather than a run's.

A release the Lead Product Manager decides has a work item of its own, titled
for its version, and drafting and curating its notes is that item's reviewed
run. The run's change lands the file on `main` through review like any other
change. A person writes and commits the file only for a release they cut by
hand.

Only what the tracker calls **closed** reaches the notes. An id in a commit
message says work touched that item, not that the item is done — a parent epic
is named by every child's commit, and a multi-part item by each part as it lands
— so neither is published as shipped. What is put aside for not being closed is
counted in the output rather than dropped quietly.

Where the draft puts each item is placed from its type alone, and that placement
is a starting point rather than an answer. **Which work is key, which is an
enhancement, and which fix is critical enough to go to the top is the Lead Product
Manager's judgement**, until the post-v1 release-manager role exists. Edit the
file, then commit it.

## The readiness section

One section is not the Lead Product Manager's: the
**Release readiness** result, between the markers
`<!-- yoyodyne:release-readiness -->` and `<!-- /yoyodyne:release-readiness -->`
at the end of the file. It is what
[`yoyo conformance`](../artifacts.md#asking-all-of-this-at-once-before-a-tag)
found when the cut ran ahead of the tag — the artifacts, the references, the
links, the invariants, the goal every admitted item serves, and what a change
upstream left unanswered downstream — so a published release says what was
true of the tree it names rather than only that its tests passed.

For a release the Lead Product Manager decided, the release item's reviewed run
writes it: it puts the section `yoyo conformance --notes` prints between those
markers and lands it with the notes. For a release a person cuts with
`make release`, the cut writes it. The cut does not write it to `main` itself:
it commits the stamped notes on a branch and opens a pull request, which is how
the result reaches this file, and the cut after the merge tags. The harness's
own cut, under `approvals.releases: automatic`, opens nothing. A result that is
missing or no longer current refuses that cut, and the release goes back to
the Lead Product Manager for its run to stamp again. It replaces what is between those two
markers and leaves everything around them alone, so a second stamp does not
leave two results that disagree, and a result already here with the same
verdict from the same definition is left alone rather than rewritten for its
counts. Editing inside them is editing a record of what a check found, which
the next stamp overwrites; everything you want to say about the release goes
in the sections above.

[Cutting a release](../developing-yoyo.md#cutting-a-release) gates on this file
being present: `make release VERSION=<tag>` with no notes for `<tag>` drafts
them and refuses, so the judgement happens before the tag rather than after the
release page is published. The notes commit has to reach `origin/main` before
the cut will go through, because the cut refuses a `HEAD` the remote does not
have; and then the readiness result has to reach it too, through the pull
request the cut opens, before the cut tags.

The notes and their stamp on `main` are the preparation, not the release. A
release is its tag on origin. When the harness cuts one, the tag's push is
recorded on the release item, naming the commit the tag names and the gates
that passed on it. That note, not the notes file, is what the Lead Product
Manager closes the item on.
[Cutting a decided release](../operations.md#cutting-a-decided-release) is the
harness's path, and [cutting a release](../developing-yoyo.md#cutting-a-release)
is the manual one.
