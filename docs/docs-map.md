# The documentation map

Drafted for yoyodyne-ifd.121.1. **Status: draft — the architect's voice on
anchor stability and cross-link structure, and the operator's approval, are
still outstanding.** It lives outside the artifact homes for the same reason
[team mode scope](team-mode-scope.md) does: every document inside one carries
identity frontmatter, and no governed kind exists yet for a structural
decision. When the artifact contract grows one, this document moves under it.

This is the single structure decision behind two splits — the README split
(yoyodyne-ifd.121.2) and the configuration guide split (yoyodyne-ifd.117). It is
one document rather than two so the link map lands once and the README never
links into an anchor its sibling split then moves. Each execution run cites the
section here that it implements, and does not decide structure of its own.

The problem it answers: at drafting the README was 152KB across 2,602 lines and
the configuration guide 132KB across 2,377. Both have grown since — re-measured
on 2026-08-24 for yoyodyne-ifd.166, the README is 3,792 lines and the
configuration guide 2,989. The adoption goal — *a newcomer can go from the
documented install to a working first run on their own repository using the
readme alone* — is failed by a document nobody reads to the end, and the
configuration guide costs more than a whole context budget to include.

**The README split is complete.** yoyodyne-ifd.160 trimmed the README to 752
lines on 2026-08-24 by deleting the bodies the six documents below already
carried; tranches yoyodyne-ifd.121.4 and yoyodyne-ifd.121.5 reduced what was
left against those documents; and yoyodyne-ifd.121.6 closed the sequence on
2026-09-05 with the repository-wide citation sweep this document's tables now
record, and with the getting-started walkthrough made a merge gate rather than
a habit — the `getting-started` job in `.github/workflows/ci.yml` installs `bd`
and runs `make getting-started` on every pull request, so the claim the README makes about its
own getting-started section is executed on every change.

The README stood at 844 lines at the end of the split: 600 of them the README
the split was aiming at, and 244 the `## Configuring a project` block held back
for the configuration split — see [the disposition
table](#disposition-of-every-current-readme-section). **yoyodyne-ifd.437.13
rewrote it on 2026-09-26 to 344 lines (15 KB, from 45 KB)**, taking that block
out with the rest; [the rewrite's own
table](#the-2026-09-26-rewrite-what-left-the-readme-and-where) records every
move. The configuration guide has not been split. It was 3,848 lines at the end
of the README split, the split-era figure its tables below were re-measured
against, and measured on 2026-09-27 it is 6,770 lines, so those tables describe
a file little more than half its current size. The disposition table's own
section counts are older still, drawn from the 2,989-line file of 2026-08-24.

## What each document is for

Audience is the split's only organizing principle. A section moves to the
document whose reader needs it, not to the one whose subsystem it names.

| Document | Audience | Roughly |
|---|---|---|
| [`README.md`](../README.md) | Someone deciding whether to use yoyo, and then reaching a first run | 500 lines |
| `docs/conversation.md` | An operator driving work from `yoyo chat` | 816 lines |
| `docs/work.md` | An operator who has approved work and wants to know what happens to it | 270 lines |
| `docs/artifacts.md` | An operator maintaining the brief, goals, designs, and invariants | 246 lines |
| `docs/reporting.md` | An operator asking what the work cost and what came back | 244 lines |
| `docs/operations.md` | An operator recovering from a stall, a crash, or a provider refusal | 454 lines |
| `docs/developing-yoyo.md` | Someone changing yoyo itself | 30 lines |
| [`docs/provider-plugins.md`](provider-plugins.md) | Someone running yoyo on a fork, proxy, or variant of a provider it already speaks | 288 lines |
| [`docs/configuration.md`](configuration.md) | Anyone arriving at a configuration link — an index and the frozen anchors | 120 lines |
| `docs/configuration/setup.md` | Someone writing, inheriting, or validating a project configuration | 524 lines |
| `docs/configuration/artifacts.md` | Someone configuring artifact homes, approval, and ownership | 549 lines |
| `docs/configuration/goals.md` | Someone configuring admission, attribution, and staleness | 440 lines |
| `docs/configuration/runs.md` | Someone configuring checks, scheduling, and what a run costs | 343 lines |
| `docs/configuration/publishing.md` | Someone configuring pull requests, branches, and merges | 226 lines |
| `docs/configuration/recovery.md` | Someone configuring triage thresholds and provider-refusal waits | 513 lines |
| `docs/configuration/agents.md` | Someone configuring the agents and the humans: accounts, operators, personas, exchanges, research sources, and Slack | 370 lines |

Line counts are the current content's, carried across. They are a size budget
for review, not a target to write to: a run that lands a document materially
larger than its budget has found content the map misplaced, and should say so
rather than pad or trim to fit.

**The seven `docs/configuration/` budgets were re-measured on 2026-08-24** for
yoyodyne-ifd.166, and they are the sums of the reconciled disposition table
below rather than the drafting-time figures: every one of the seven moved,
because half the section counts they were summed from were undercounts and four
sections had no row at all. The `docs/configuration.md` row is the exception —
it is an estimate for prose the split writes rather than content carried across,
so it is left where drafting put it.

**The six README-split documents have landed**, and their budgets are spent
rather than pending. Re-measured on 2026-09-05, at the end of the split:
`conversation.md` at 1,363 lines against 816, `operations.md` at 1,169 against
454, `reporting.md` at 816 against 244, `work.md` at 651 against 270,
`developing-yoyo.md` at 377 against 30, and `artifacts.md` at 363 against 246.
Every one overran, mostly because the README itself grew by roughly 1,200 lines
between drafting and execution, and each grew again across tranches 121.3 to
121.5 as content the README's reduction surfaced was written up properly rather
than dropped. They are recorded here as landed facts; nothing should be trimmed
to reach the figure in the table. `provider-plugins.md`, which the split
neither wrote nor moved content into, is at 308 lines against 288.

One document is reachable from the index below and is deliberately not a row
here: [`docs/releases/README.md`](releases/README.md), which explains the notes
home one file per tag lives in. It carries none of the content this map moves,
so it has no budget and no destination — it is registered in the further-reading
index so the split does not land a README that cannot reach it.

## The moved-anchor policy

Stated once, up front, so no execution run discovers it. Three tiers, and the
tier is a property of who cites the anchor rather than of what the section says.

**Tier 1 — frozen.** The anchor survives at its current path forever, as a
redirect stub: a heading with the same slug whose body is one sentence and a
link to the canonical home. Never renamed, never deleted, even when the content
under it has moved. An anchor is Tier 1 when it is cited by something this
repository cannot rewrite:

**The list below was re-derived on 2026-09-05** by yoyodyne-ifd.121.6's sweep,
and it is materially longer than the three anchors drafting found. The method is
three searches, and it is stated so a later run re-derives rather than trusts:
`configuration.md#` and `README.md#` across the whole tree; the same two across
`.beads/issues.jsonl`, the tracker's export; and every citation of any `.md#`
anchor made from inside `docs/product/`, `docs/designs/`, `docs/decisions/`, or
`.yoyodyne/`.

- **Shipped artifacts**, which are on disks we do not control or in text already
  published. Three anchors, from two sources:
  - `docs/configuration.md#checks` — `internal/config/scaffold.go:414`, asserted
    by `internal/cli/init_test.go:302`. Every `.yoyodyne/config.yaml` that
    `yoyo init` has ever generated points at it, in a file its owner edits.
    `internal/config/scaffold.go:660` now cites
    `docs/configuration.md#provider-accounts` the same way, from the accounts
    block the same generated file carries, so that anchor is Tier 1 on the same
    argument.
  - `README.md#getting-started` — `.github/release-notes-preamble.md:28`, as the
    repo-root URL `https://github.com/mason-bryant/yoyodyne#getting-started`.
    `scripts/release-body.sh` appends that preamble to the tag's own
    `docs/releases/<tag>.md`, and `.github/workflows/release.yml` passes the
    result to `gh release create --notes-file`. It is in the notes of every
    release already published, and published release notes cannot be corrected
    by a change to this repository. **This one is enforced rather than recorded.**
    `internal/doclink` resolves a URL naming this repository's own forge home
    against the checkout — the repo-root form against `README.md`, the blob form
    against the file it names — so moving or renaming that heading fails
    `make test` with the preamble's line named. `TestThePublishedReleaseNotesLinkIntoTheReadmeIsHeldByThisChecker`
    holds the coverage itself, so the freeze cannot be lost by a change to the
    checker either.
- **Tracked work items.** The backlog is upstream: the Lead Product Manager owns it,
  and no developer rewrites an item to chase a link. **The tracker mentions six
  configuration-guide anchors, and four of them are Tier 1 because of it** —
  unlike at drafting, one of the citing items is open rather than closed:

  - **The four.** `docs/configuration.md#merge-and-removal-semantics`,
    `#what-fails-closed`, `#what-init-proposes-for-checks`, and
    `#what-reaches-the-queue`, all from yoyodyne-ifd.117.1, which is open and is
    the configuration split's own first tranche.
  - **The other two are mentioned and not frozen by it.**
    `#product-specifications` in yoyodyne-ifd.20.2 and yoyodyne-ifd.21, and
    `#checks` in yoyodyne-ifd.159, all closed. Both anchors are Tier 1 anyway on
    other grounds — a design cites the first and generated configuration cites
    the second — so nothing turns on which way the tracker is read for either.
    The next paragraph is why they are listed apart.

  **What counts as a citation here, since the tracker holds two different
  things.** An item's own text — its description, its design guidance, its
  acceptance criteria — is a citation: a run that has not happened yet will
  follow it, and a renamed anchor makes it unfollowable by somebody who cannot
  repair it. A closed item's recorded review findings and review summaries are
  not: they are an account of what was true when the review ran, and they stay
  true as an account whatever later becomes of the anchor. So the tier turns on
  whether the item is still work somebody will act on, and the four above are
  frozen because ifd.117.1 is open and names those anchors as the ones its next
  tranche must handle. Applying it the other way — freezing on a closed item's
  transcript — would freeze an anchor for the sake of a sentence nobody will ever
  follow, which is the stub graveyard [the architect was asked
  about](#what-the-architect-is-being-asked) and told this policy to avoid.
  This distinction is yoyodyne-ifd.121.6's, drawn because the README sweep
  below turns on it; the architect should confirm or overturn it when they take
  up the questions at the end of this document.
- **Protected artifact homes** — `docs/product/`, `docs/designs/`,
  `docs/decisions/`, and `.yoyodyne/`. A developer on this effort may not edit a
  file in one, so an anchor cited from inside one is Tier 1 by construction: the
  only alternative is a proposal to an owner who may decline it, which would
  leave the link dangling in the meantime. Six anchors today, all of them
  `docs/configuration.md`'s:
  - `#product-specifications`, from `docs/designs/v1-harness-design.md:283`.
    Drafting also named `docs/product/goals/README.md:11`; that file now cites
    `v1-harness-design.md#artifact-ownership` instead and no longer cites the
    configuration guide at all.
  - `#precedence`, `#merge-and-removal-semantics`, `#what-fails-closed`, and
    `#converting-an-inheriting-configuration-to-an-explicit-one`, all from
    `docs/designs/portable-agent-configuration.md` — `:33`, `:34`, `:35`, and
    `:45` and `:145`. That design did not exist when this policy was drafted,
    and it alone quadruples the protected-home set.
  - `#running-a-work-item-against-the-workflow-definition`, from
    `.yoyodyne/workflows/delivery.yaml:8`. This is the one citation in the whole
    sweep that is neither Markdown nor Go, so nothing mechanical checks it: the
    link checker described [below](#what-the-split-breaks-that-neither-item-mentions)
    reads Markdown files, and a fragment in a YAML comment is invisible to it.
    The section it names — `## Running a work item against the workflow
    definition`, `docs/configuration.md:1935` — also has no row in the
    disposition table further down, so the configuration split has to place it
    as well as freeze it.

  Note the overlap, because it changes what the configuration split has to
  build: `#merge-and-removal-semantics` and `#what-fails-closed` are Tier 1
  twice over, from the design and from an open work item.

**Tier 2 — updated in the same change.** The anchor is cited only by files in
this repository, so the run that moves the section rewrites every citation in
the same commit. No stub; a stub for an anchor we can fix is debt with no
creditor. Every Tier 2 citation is enumerated below.

**Tier 3 — free.** Nothing cites the anchor. Move it without ceremony.

**The rule underneath all three: no change in this effort may leave a fragment
that resolves to nothing.** A moved anchor is either frozen or re-cited, and
"the link still opens the right file, just at the top of it" does not count —
GitHub ignores an unknown fragment silently, so a reader lands on an index with
no sign anything went wrong. That is the failure mode this policy exists to
prevent, and it is invisible to exactly the reviewer who would catch a 404.

**Uniqueness is not required across documents.** `## What a change upstream
leaves stale` and `## Architectural invariants` each exist today in both the
README and the configuration guide, and after the split each pair lands in two
different files. That is fine and intended: one is the narrative for an
operator, the other is the reference for someone editing configuration. Each
must link to the other, so a reader who arrives at the wrong one gets to the
right one in a click.

## Every anchor citation, and where it goes

Four sets, and all four are enumerated: citations of `docs/configuration.md`
anchors, citations of `README.md` anchors, and the links each of the two
documents makes into itself. Nothing is scoped out. The method that produced
them is stated with each set so the executing run can re-derive rather than
trust, because anything landed between drafting and execution adds rows.

### Citations of `docs/configuration.md` anchors

With the document each moves to and its tier. **Re-derived on 2026-09-05** by
yoyodyne-ifd.121.6, the README split's last tranche, and this is the third time
the table has been re-derived and the third time every line number in it had
moved. Re-derive it the same way: search `configuration.md#` across the whole
tree, then across `.beads/issues.jsonl`, and read the two protected homes
separately, because a citation from one of those is Tier 1 whatever the anchor
says.

**Superseded by the 2026-09-26 rewrite; do not execute against this set.** The
README now cites six anchors, once each: `#recurring-tasks`,
`#where-the-tracker-syncs`, `#what-init-proposes-for-checks`,
`#how-long-a-check-may-take`, `#keeping-the-configuration-outside-the-repository`,
and `#publishing-through-pull-requests`. `#provider-accounts`, `#checks`, and
`#when-the-repository-ignores-the-configuration` are no longer among them, and
`#recurring-tasks` has no row in the tier table below. Before the rewrite, the
README's own citations were these seven, across six anchors:

| README line | Anchor |
|---|---|
| `:344` | `#where-the-tracker-syncs` |
| `:386` | `#checks` |
| `:398` | `#how-long-a-check-may-take` |
| `:423` | `#when-the-repository-ignores-the-configuration` |
| `:549` | `#publishing-through-pull-requests` |
| `:574` | `#where-the-tracker-syncs` |
| `:707` | `#provider-accounts` |

One more than the last pass found, and the new one matters: `#provider-accounts`
is cited from `### Running several Claude accounts`, a README section that has
never had a row in the disposition table at all. It was inside the held-back
`## Configuring a project` block, which the 2026-09-26 rewrite took out of the
README — but it had to be found rather than assumed, and finding it is what
added its row below.

**Twenty-five of the thirty-one rows have no README citation at all**, the
section that made it having moved into one of the six landed documents. That is
expected and is not a defect: the citation moved with the prose rather than
disappearing, and each row's `Cited by` column names where it went.

**Twelve anchors below had no row before this pass**, which is the largest single
correction this table has taken. Four come from
`docs/designs/portable-agent-configuration.md`, a design that did not exist when
the policy was drafted, and one from `.yoyodyne/workflows/delivery.yaml` — so
five of the twelve are Tier 1 by protected home alone. Four more name sections
`docs/configuration.md` has grown since the last pass —
`#the-release-readiness-workflow`,
`#crossing-a-cap-the-operator-decides-to-cross`,
`#pooling-work-across-several-accounts`, and
`#running-a-work-item-against-the-workflow-definition` — and none of those four
has a destination row in [the disposition
table](#what-the-configuration-guide-becomes) either.

| Anchor | Cited by | Tier | New home |
|---|---|---|---|
| `#checks` | `README.md:386`, `docs/developing-yoyo.md:179`, `internal/config/scaffold.go:414`, `internal/cli/init_test.go:302`, `internal/doclink/doclink.go:30`, `:308`, and yoyodyne-ifd.159 | **1** | stub stays; canonical `configuration/runs.md#checks` |
| `#product-specifications` | `docs/designs/v1-harness-design.md:283`, and yoyodyne-ifd.20.2 and yoyodyne-ifd.21 | **1** | stub stays; canonical `configuration/artifacts.md#product-specifications` |
| `#provider-accounts` | `README.md:707`, `docs/multi-account-quickstart.md:6`, `internal/config/scaffold.go:660` | **1** | stub stays; canonical `configuration/agents.md#provider-accounts` |
| `#precedence` | `docs/designs/portable-agent-configuration.md:33` | **1** | stub stays; canonical `configuration/setup.md#precedence` |
| `#merge-and-removal-semantics` | `docs/designs/portable-agent-configuration.md:34`, and yoyodyne-ifd.117.1 (open) | **1** | stub stays; canonical `configuration/setup.md#merge-and-removal-semantics` |
| `#what-fails-closed` | `docs/designs/portable-agent-configuration.md:35`, and yoyodyne-ifd.117.1 (open) | **1** | stub stays; canonical `configuration/setup.md#what-fails-closed` |
| `#converting-an-inheriting-configuration-to-an-explicit-one` | `docs/designs/portable-agent-configuration.md:45`, `:145` | **1** | stub stays; canonical `configuration/setup.md` |
| `#running-a-work-item-against-the-workflow-definition` | `.yoyodyne/workflows/delivery.yaml:8` | **1** | stub stays; **no destination row yet** |
| `#what-reaches-the-queue` | `docs/artifacts.md:44`, `docs/conversation.md:167`, `:215`, `:257`, `:320`, `:770`, and yoyodyne-ifd.117.1 (open) | **1** | stub stays; canonical `configuration/goals.md` |
| `#what-init-proposes-for-checks` | yoyodyne-ifd.117.1 (open), and one intra-document link | **1** | stub stays; canonical `configuration/runs.md` |
| `#where-the-tracker-syncs` | `README.md:344`, `:574` | 2 | `configuration/setup.md` |
| `#when-the-repository-ignores-the-configuration` | `README.md:423` | 2 | `configuration/setup.md` |
| `#extending-a-built-in-bundle` | `docs/reporting.md:727`, `docs/slack/setup.md:757` | 2 | `configuration/setup.md` |
| `#how-long-a-check-may-take` | `README.md:398` | 2 | `configuration/runs.md` |
| `#scheduling-ready-work` | `docs/work.md:374` | 2 | `configuration/runs.md` |
| `#publishing-through-pull-requests` | `README.md:549`, `docs/work.md:651` | 2 | `configuration/publishing.md` |
| `#losing-a-race-for-the-target-branch` | `docs/operations.md:1041` | 2 | `configuration/publishing.md` |
| `#approving-a-document` | `docs/artifacts.md:51` | 2 | `configuration/artifacts.md` |
| `#protected-paths-in-a-developers-change` | `docs/conversation.md:1098`, `docs/work.md:69` | 2 | `configuration/artifacts.md` |
| `#what-the-lead-product-manager-sees-besides-them-and-what-it-does-not` | `docs/conversation.md:99` | 2 | `configuration/artifacts.md` |
| `#traceability-references-and-orphans` | `docs/artifacts.md:85` | 2 | `configuration/goals.md` |
| `#what-a-change-upstream-leaves-stale` | `docs/artifacts.md:170` | 2 | `configuration/goals.md` |
| `#the-release-readiness-workflow` | `docs/artifacts.md:355`, `docs/developing-yoyo.md:300` | 2 | **no destination row yet** |
| `#what-one-work-item-has-been-given` | `docs/conversation.md:996`, `docs/operations.md:1061` | 2 | `configuration/recovery.md` |
| `#crossing-a-cap-the-operator-decides-to-cross` | `docs/conversation.md:998` | 2 | **no destination row yet**; its parent lands in `configuration/recovery.md` |
| `#personas` | `docs/conversation.md:76` | 2 | `configuration/agents.md` |
| `#operators` | `docs/slack/setup.md:231`, `:1176` | 2 | `configuration/agents.md` |
| `#avatars` | `docs/slack/setup.md:203` | 2 | `configuration/agents.md` |
| `#research-sources` | `docs/conversation.md:137` | 2 | `configuration/agents.md` |
| `#how-long-one-role-may-ask-another` | `docs/conversation.md:879` | 2 | `configuration/agents.md` |
| `#pooling-work-across-several-accounts` | `docs/multi-account-quickstart.md:7` | 2 | **no destination row yet**; its parent lands in `configuration/agents.md` |

Three rows say **no destination row yet**, and those three are the sharpest
result of this pass: the section exists, something outside the guide links to
it, and the table that says where every section goes has never named it. A
fourth, `#running-a-work-item-against-the-workflow-definition`, is in the same
state and is Tier 1 besides. Each is the standing argument for re-deriving this
table at the top of each execution run rather than trusting it — the third
consecutive pass at which trusting it would have dropped content other documents
point at.

The forge-URL spelling of a citation was swept too, since a link written as
`https://github.com/mason-bryant/yoyodyne/blob/main/docs/…#anchor` matches
neither of the searches above. It finds only what is already here:
`.github/release-notes-preamble.md:28` for `README.md#getting-started` and
`internal/config/scaffold.go:414` for `docs/configuration.md#checks`, both
already Tier 1.

### Citations of `README.md` anchors

Twenty-two README sections move out to seven new documents, so this set has to
be established rather than assumed. It was swept three ways: every
`README.md#…` reference in any case anywhere in the tree; every GitHub URL for
this repository carrying a fragment, which is how a link to the README's
rendered root is written; and every README slug whose section moves, searched
literally as `#slug` across the working tree and the tracker's current export.

The result is small, and worth stating flatly because the map's later claim that
the README keeps four things and needs no stub of its own depends on it. **The
sweep was re-run on 2026-09-05** by yoyodyne-ifd.121.6 and this table is its
result. It has six rows where the last pass had five, and every line number in
it had moved again.

| Anchor | Cited by | Tier | New home |
|---|---|---|---|
| `#further-reading` | `skills/yoyo-setup/SKILL.md:21`, `internal/doclink/doclink.go:354`, and the back-link each of `docs/conversation.md:4` (and `:94`), `work.md:4`, `artifacts.md:4`, `reporting.md:4`, `operations.md:4`, `developing-yoyo.md:4` (and `:179`), `delivery-pipeline-baseline.md:4`, `releases/README.md:4`, and `configuration.md:539` opens with | — | section stays; no move to service |
| `#getting-started` | `.github/release-notes-preamble.md:28`; **recorded prose** in closed yoyodyne-ifd.156 and yoyodyne-ifd.159 | **1** | stays in the README — Tier 1 on the preamble alone |
| `#3-yoyo-chat--establish-the-brief-and-the-goals` | `skills/yoyo-setup/SKILL.md:220`; **recorded prose** in closed yoyodyne-ifd.125.4 | — | section stays; no move to service |
| `#keeping-the-configuration-out-of-the-repository` | `docs/configuration.md:153`; **recorded prose** in closed yoyodyne-ifd.76 | 2 | **retired** by the 2026-09-26 rewrite; content in `docs/configuration.md#keeping-the-configuration-outside-the-repository` |
| `#running-several-claude-accounts` | `docs/configuration.md:3460` | 2 | **retired** by the 2026-09-26 rewrite; content in `docs/multi-account-quickstart.md` |
| `#talking-to-the-other-agents` | nothing in the working tree; **recorded prose** in open yoyodyne-ifd.117.1 and closed yoyodyne-ifd.121.2 | — | **spent** — repointed by ifd.160 to `conversation.md#talking-to-the-other-agents` |

**Six items mention a README anchor, and none of the six freezes one.** Every
one of the six mentions it inside recorded review findings or a review summary
rather than in the item's own description, guidance, or criteria, and five of the
six are closed:

- **ifd.156, ifd.159** (`#getting-started`), **ifd.125.4**
  (`#3-yoyo-chat--establish-the-brief-and-the-goals`), **ifd.76**
  (`#keeping-the-configuration-out-of-the-repository`), **ifd.121.2**
  (`#talking-to-the-other-agents`) — all closed. ifd.76's is worth spelling out
  because it is the one whose section moves: it is a review summary observing
  that `docs/configuration.md`'s link "matches the new heading's slug", which is
  a statement *about* a citation the same table row already carries, from the
  file that will rewrite it. Freezing the anchor on it would freeze the README
  for the sake of a closed reviewer's sentence describing a link this repository
  can fix.
- **ifd.117.1** (`#talking-to-the-other-agents`) — open, and the only open one.
  It quotes the anchor inside a recorded review finding about
  `docs/configuration/agents.md`, and the anchor is already gone, which a
  paragraph below names rather than repairs.

Under the rule as [applied above](#the-moved-anchor-policy), none of the six is
an item's own text that a later run follows, so **no README anchor is Tier 1 by
tracker citation.** The precedent for reading them this way is already at the
foot of this section: yoyodyne-ifd.54 and yoyodyne-ifd.1.2 have been treated as
recorded prose rather than as citations since drafting. The reading itself is
[question 2 for the architect](#what-the-architect-is-being-asked), because the
README's no-stub conclusion is what rests on it.

**The fourth and fifth rows are spent.** When this sweep ran, they named the
only sections that still had to move, each cited by exactly one live citer,
`docs/configuration.md` — the document those sections were to merge into.
ifd.160 had held `## Configuring a project` and its two children back in the
README because their destination did not exist yet. The 2026-09-26 rewrite
(yoyodyne-ifd.437.13) took all three out of the README instead and rewrote every
citing file in the same change, so `#configuring-a-project`,
`#running-several-claude-accounts`, and
`#keeping-the-configuration-out-of-the-repository` are retired rather than
waiting on the configuration split: [the rewrite's
table](#the-2026-09-26-rewrite-what-left-the-readme-and-where) says where each
one's content went.

So `#getting-started` is still the sole Tier 1 README anchor, frozen by the
release preamble rather than by anything in the backlog, and the section it names
stays — so **the README acquires no stub**. The judgement this rested on was
whether a backlog *item's own text* cited
`#keeping-the-configuration-out-of-the-repository` or
`#running-several-claude-accounts`; the 2026-09-27 search before the rewrite
landed found them, and `#configuring-a-project`, only as quoted text in recorded
review notes, so no stub is owed for any of the three.

One Go file cites a README anchor, twice: `internal/doclink/doclink.go:354` and
`:363`, both comments using `../README.md#further-reading` as the worked example
of how a relative fragment resolves, so neither is a claim about structure. No
design, `docs/product/`, `docs/decisions/`, or `.yoyodyne/` file cites a README
anchor at all.

One citation in the tracker resolves to nothing, and it is named here rather
than repaired because the backlog is upstream: **yoyodyne-ifd.117.1 is open and
its notes cite `README.md#talking-to-the-other-agents`**, an anchor ifd.160
deleted. It is quoted prose inside a recorded review finding rather than a link
anything follows, and the same finding names the live target, so nothing a
reader clicks is broken — but it is a live item pointing at a dead anchor, and
only the Lead Product Manager can correct it.

Two near-misses the sweep turned up, recorded so a re-derivation does not
re-litigate them. Several intra-file links in `docs/configuration.md` match
README slugs as prefixes — `#artifact-identity-and-metadata`,
`#architectural-invariants`, `#what-a-change-upstream-leaves-stale`,
`#provider-accounts`, `#personas` — but each resolves against
`configuration.md`'s own headings, so they belong to the set below rather than to
this one. And yoyodyne-ifd.54 and yoyodyne-ifd.1.2 mention README anchors in the
recorded prose of closed review findings, not as links anything resolves.

That ifd.54 note is worth reading, because it is this policy's precedent: it
records that a link "to `#releasing-a-usage-limit-wait-early` or
`#waiting-out-a-provider-usage-limit` is now dead" after a README rename, and
tells a later run to grep for both old anchors. README anchors have been broken
by a rename here before, and were found by hand afterwards rather than by a
check.

### Links each document makes into itself

An intra-file `](#slug)` link survives a split only if its target lands in the
same new document. Both sources have many, and they are the largest category by
count in this effort — 62 links as of 2026-09-05, down from the 91 counted on
2026-08-24 only because the README's share of them left with the sections that
carried them — so they are the likeliest thing for an execution run to miss.

**The README's share of that is settled.** It linked into itself 54 times across
32 anchors at drafting, 10 times across 9 anchors after yoyodyne-ifd.160's trim
and on 2026-09-05, and 6 times across 6 anchors after the 2026-09-26 rewrite:
once each `#install`, `#getting-started`, the three numbered step headings, and
`#optional-publishing-and-auto-merge`. The two links into the held-back block,
`#configuring-a-project` and `#keeping-the-configuration-out-of-the-repository`,
went with that block. Every other anchor became a relative link into one of the
six documents, or went with the section that carried it, and
`TestThisRepositoryOwnDocumentationLinksResolve` passes over the result — so
this line is now checked on every run rather than asserted here.

**`docs/configuration.md` links into itself 52 times across 29 anchors**, up from
37 across 24, and after the split most cross a document boundary. Resolve each
against the
configuration disposition table: a link whose target lands in the same new
document stays a bare `#slug`; one whose target lands elsewhere becomes a
relative link. Re-counted on 2026-09-05, the anchors are
`#waiting-out-a-network-that-dropped` (×5),
`#keeping-the-configuration-outside-the-repository` (×4),
`#who-may-change-an-artifact` (×3), `#what-one-work-item-has-been-given` (×3),
`#watching-instead-of-draining` (×3),
`#proposing-a-change-to-a-document-you-do-not-own` (×3),
`#artifact-identity-and-metadata` (×3), `#what-reaches-the-queue` (×2),
`#traceability-references-and-orphans` (×2), `#operators` (×2), `#layout` (×2),
`#goals-and-the-work-attributed-to-them` (×2), `#approving-a-document` (×2), and
one each of `#what-init-proposes-for-checks`, `#what-fails-closed`,
`#what-a-change-upstream-leaves-stale`, `#the-definition-is-the-projects-to-own`,
`#reporting-to-slack`, `#relaunching-a-run-the-provider-killed`,
`#publishing-from-a-fork`, `#provider-accounts`,
`#protected-paths-in-a-developers-change`, `#product-specifications`,
`#personas`, `#losing-a-race-for-the-target-branch`,
`#how-long-a-check-may-take`, `#extending-a-built-in-bundle`,
`#crossing-a-cap-the-operator-decides-to-cross`, and
`#architectural-invariants`.

Nothing dropped off this list since the last pass — all 24 anchors it named are
still linked — and five are new to it: `#waiting-out-a-network-that-dropped`,
`#keeping-the-configuration-outside-the-repository`,
`#the-definition-is-the-projects-to-own`, `#publishing-from-a-fork`, and
`#crossing-a-cap-the-operator-decides-to-cross`. Three of the five name sections
the disposition table has no row for at all — the first, the second, and the
third, whose parent `## Running a work item against the workflow definition` has
none either. `#waiting-out-a-network-that-dropped` is the guide's most-linked
anchor of all at five links, and it names a section the table below has never
heard of, which is the clearest single measure of how far the file has moved.

`#provider-accounts` remains the case worth naming: it is linked from
`## Layout`, which lands in `configuration/setup.md`, while its target lands in
`configuration/agents.md`, so it has to become a relative link.

## What the README becomes

The README keeps three things and links out for everything else: the value
proposition, everything a newcomer needs to reach a working first run, and the
index. It kept a fourth, the testimonials, until the 2026-09-26 rewrite
(yoyodyne-ifd.437.13) removed them. It keeps **nothing extra to service a link** — no
Tier 1 stub of its own — and that is a result of the sweep above rather than an
assumption. The two README anchors whose sections were held back for the
configuration split, and `#configuring-a-project` above them, were retired by the
2026-09-26 rewrite after every file citing them was rewritten in the same change.
Nothing this repository cannot rewrite cited any of them, and the tracker's
mentions of them are [recorded prose rather than
citations](#citations-of-readmemd-anchors), so **no README stub is owed**.

Install and Getting started **stay in the README**, for two independent reasons.
The first is the goal: a newcomer reaches a working first run *using the readme
alone*, and a README that sends someone to another document between installing
and running fails that goal in its own words, however brief it reads. The second
is that `#getting-started` is Tier 1 — every published release's notes link to
it — so that section is pinned where it is whatever the first argument decides.
A later restructure that finds the goal argument unpersuasive still may not move
it without leaving a stub.

This is the one place the map spends its size budget deliberately: the README
lands at roughly 500 lines rather than the 150 a pure landing page would be, and
the sections below the quick start are what carry the weight of the reduction.

**What it actually landed at, on 2026-09-05: 600 lines**, counting the whole file
and then deducting the 244 lines of `## Configuring a project` and its two
children, which were then held back for the configuration split. That is a 20% overrun
on a 500-line budget the map itself says is a size budget for review rather than
a target to write to, and the reason is the one the budget was set against: the
README the split started from was 3,792 lines rather than the 2,602 the budget
was drawn from. The 844 lines the file then held were meant to fall to 600 when
the configuration split took the block it was owed. That is superseded: the
2026-09-26 rewrite took the block out itself and brought the README to 344
lines, recorded in [its own
table](#the-2026-09-26-rewrite-what-left-the-readme-and-where).

Target order. **The line ranges below are the README as it stood before
yoyodyne-ifd.160 executed this section; they are the plan's own citations rather
than a description of the file as it now reads**, and the trimmed README followed
this order with `## Configuring a project` held back between steps 8 and 9 until
the 2026-09-26 rewrite removed it:

1. `# yoyo` — the value proposition, unchanged (README.md:1–31).
2. **User testimonials**, unchanged (:32–49).
3. **Three gates**, unchanged (:50–64) — the shortest honest statement of what
   makes the thing trustworthy, and it belongs before the quick start.
4. **You drive it from one conversation** (:65–83), trimmed to the paragraph and
   its command names, with the links out repointed at the new documents.
5. **Quick start**, unchanged (:84–98).
6. **What exists today is bounded** (:99–121), unchanged — the bounds are worth
   knowing before you start rather than after, and that reasoning is unaffected.
7. `## Install` — unchanged (:122–192).
8. `## Getting started` — the three steps, `Optional: publishing and auto-merge`,
   unchanged except for repointed links (:193–442).
9. `## Further reading` — replaced by the index below.

`### Working on yoyo itself` leaves the README for `docs/developing-yoyo.md`. It
is contributor material, and the README's reader is not yet a contributor.

The new index, which is the link map made concrete and the only genuinely new
prose the README split writes:

```markdown
## Further reading

**Driving the work**
- [The conversation](docs/conversation.md) — proposals and batches, steering,
  directives, the other agents, and what the conversation looks like on a terminal.
- [How work flows](docs/work.md) — what happens after you approve an item, letting
  the harness choose, reviewing a branch, and publishing.
- [What comes back to you](docs/reporting.md) — what the work cost, what agents
  report and propose, and reporting into Slack.

**Your written intent**
- [Artifacts, goals, and invariants](docs/artifacts.md) — artifact identity, the
  goal a work item names, what a change upstream leaves stale, and architectural
  invariants.

**When something goes wrong**
- [Operations and recovery](docs/operations.md) — pausing and resuming, provider
  limits and stalls, recovering interrupted runs, and following a run.

**Reference**
- [The configuration guide](docs/configuration.md) — the full reference, split by
  audience, with an index.
- [The v1 harness design](docs/designs/v1-harness-design.md) — the architecture,
  the artifact and agent models, and the Git model.
- [Reporting into Slack](docs/slack/setup.md) — an empty workspace to live
  reporting in threads.
- [Release notes](docs/releases/README.md) — one file per tag, what each section
  is for, and how a cut drafts one from the work that landed.
- [`docs/product/`](docs/product) — the brief, the goals, and everything else filed there, which every role reads as authoritative product intent.
- [Working on yoyo itself](docs/developing-yoyo.md) — the checks, the build, and
  what a release is.
```

### The 2026-09-26 rewrite: what left the README, and where

yoyodyne-ifd.437.13 rewrote the README from 45 KB to 15 KB on the Lead Product
Manager's review of that day, keeping the order a newcomer needs: what it is,
the three gates, a quick start with `yoyo setup` first, what is bounded today,
`## Install`, `## Getting started` with its three steps and `### Optional:
publishing and auto-merge`, and `## Further reading`. The Tier 1 anchor
`#getting-started` is unchanged, and so are `#further-reading`, `#install`,
`#optional-publishing-and-auto-merge`, and the three step anchors, which
`skills/yoyo-setup/SKILL.md` cites. Three README anchors were removed:
`#configuring-a-project`, `#running-several-claude-accounts`, and
`#keeping-the-configuration-out-of-the-repository`. Each file that cited one of
the last two now cites the page that holds its content. The only link to
`#configuring-a-project` was the README's own step 2, removed with the heading;
a search of the repository on 2026-09-27 finds it nowhere else except as quoted
text in recorded review notes (the tracker export and a test fixture copied from
it), which no link follows.

| What left | Where it lives now |
|---|---|
| **User testimonials** | Removed. They quoted AI tools' output about the repository as if from users. |
| The bounds' "none of them act without you" and "nothing decomposes a design on its own" | Replaced: both were false. The bounds now say the development manager decides stopped work, `yoyo work` carries it out, and recurring tasks and program managers can be configured. |
| "Days of merged work pass between them" | Replaced by what the harness brings to you. |
| The long `yoyo run` / `review` / `status` / … command sentence | `yoyo help`. |
| `yoyo setup`'s idempotence, `--yes` / `--json` detail | Kept in one sentence; the rest is `docs/operations.md#setting-up-with-yoyo-setup`, new in this change. |
| The artifact-home index paragraph | One sentence in step 2; the detail is `docs/artifacts.md#the-index-at-the-door-of-each-home`, new in this change. |
| Tracker-sync consequences (repository size, `refs/dolt/data`, team caveat) | `docs/configuration.md#where-the-tracker-syncs`. |
| The three headings `init` writes beside `checks` | One sentence in step 2; the table is `docs/configuration.md#what-init-proposes-for-checks`. |
| Check budgets and the stage bound | `docs/configuration.md#how-long-a-check-may-take` and the sections after it. |
| "If you ignored it" warning detail | `docs/configuration.md#when-the-repository-ignores-the-configuration`. |
| The `.beads/` README aside | Removed; it described a redirect that no longer needs saying. |
| "What the Lead Product Manager can see" and its eight-document list | `docs/configuration.md#what-the-lead-product-manager-sees-besides-them-and-what-it-does-not`. |
| Optional publishing's `integration: human` behaviour, dropped merges | `docs/configuration.md#publishing-through-pull-requests` and `docs/work.md`. |
| `## Configuring a project` | `docs/configuration.md`, which already carried its content. |
| `### Running several Claude accounts` (`#running-several-claude-accounts`, removed) | `docs/multi-account-quickstart.md`, which gained the browser-profile warning, the by-hand login, and the state directory. `docs/configuration.md` now links there. |
| `### Keeping the configuration out of the repository` (`#keeping-the-configuration-out-of-the-repository`, removed) | `docs/configuration.md#keeping-the-configuration-outside-the-repository`, which gained the `.git/info/exclude` route and the artifact-home index exclusion. `docs/configuration.md` and `docs/configuration/setup.md` now link there. |

Also corrected in the rewrite: `init` copies six personas, not five; the
`yoyo version` example names no fixed tag; the repository's home is
`github.com/swarmgraph/yoyodyne` in every clone and release line, and the
install lines keep the module path `github.com/mason-bryant/yoyodyne`, which
installs through GitHub's redirect; and "harness" is defined where it is first
used.

### Disposition of every current README section

Every section, with its destination. Nothing is dropped; a run that finds
content with no row here stops and reports rather than choosing a home.

**This table was executed by yoyodyne-ifd.160 on 2026-08-24, and every row
marked `stays` or naming one of the six landed documents is now spent.** The
README carries the value proposition, the gates, the quick start, the bounds, `## Install`, `## Getting started` with its three steps and
`Optional: publishing and auto-merge`, and the index — plus one section the
table did not have a landed home for, below. It also carried the testimonials
until the 2026-09-26 rewrite (yoyodyne-ifd.437.13) removed them. Its counts were the README as it
stood at 2,602 lines rather than the 3,792 it reached before the trim, and they
are left as drafted because what they were for has happened.

**One row was not executed by ifd.160, deliberately.** `## Configuring a
project` and its two children — `### Running several Claude accounts` and
`### Keeping the configuration out of the repository` — stayed in the README,
because their destination was the `docs/configuration.md` index that the
configuration split had not yet built, and dropping them ahead of it would have
lost content to no home. They were the whole of the README's overrun — 844 lines
against the 500 budget, 600 without them. **The 2026-09-26 rewrite executed
these three rows** without waiting for the split: their content went to
`docs/configuration.md` and `docs/multi-account-quickstart.md`, as [its
table](#the-2026-09-26-rewrite-what-left-the-readme-and-where) records, so the
configuration split owes the README nothing.

**`### Running several Claude accounts` had no row at all**, and
yoyodyne-ifd.121.6's sweep is what found it: it is `README.md:644–710`, 67 lines,
and it is what makes the README's citation of
`docs/configuration.md#provider-accounts` at `:707`. Its row is added below,
with the same destination as its parent, because it is the README's narrative of
the account pool and the configuration guide's `## Provider accounts` and
`### Pooling work across several accounts` are the reference for the same
settings — the same merge-rather-than-move argument the parent row makes. The
run that merges it should also decide what becomes of
[`docs/multi-account-quickstart.md`](multi-account-quickstart.md), which is a
third telling of it and cites both guide anchors.

Six sections the README acquired after drafting had no row when it was executed —
`### Bringing it an idea rather than a work item`,
`### Roles asking each other things`, `#### Where the money went`,
`#### Measuring the reviewer against itself`,
`### Who reads them, and what became of each one`, and
`### Keeping the configuration out of the repository`. The first five were
already extracted and landed in `conversation.md`, `reporting.md`, and
`work.md`, so what was missing was the record rather than the content, and the
trim deleted them with the sections around them; the sixth is one of the three
held back above.

**The table below had a row for every heading the README carried** as of
yoyodyne-ifd.121.6: the two rows that tranche added are the second and third of
the held-back block. The counts in the older rows are still the 2,602-line
README's and are left as drafted, because what they were for has happened; the
two new rows count the README as it stood on 2026-09-05. The 2026-09-26 rewrite
is recorded in [its own table](#the-2026-09-26-rewrite-what-left-the-readme-and-where)
rather than here.

| Current section | Lines | Destination |
|---|---|---|
| `# yoyo` (opening, testimonials, gates, quick start, bounds) | 121 | **stays** |
| `## Install` | 71 | **stays** |
| `## Getting started` + steps 1–3 + `Optional: publishing and auto-merge` | 250 | **stays** |
| `### Working on yoyo itself` | 27 | `developing-yoyo.md` |
| `## The conversation` | 134 | `conversation.md` |
| `### Proposals, and deciding them in batches` | 116 | `conversation.md` |
| `### Steering the work from the conversation` | 166 | `conversation.md` |
| `### What the work cost` | 92 | `reporting.md` |
| `### Directives, and the work they pause` | 85 | `conversation.md` |
| `### Talking to the other agents` (+ `#### Deciding what becomes of stopped work`) | 160 | `conversation.md` |
| `### What the conversation looks like on a terminal` | 96 | `conversation.md` |
| `### What agents report, and where it reaches you` | 62 | `reporting.md` |
| `### What agents propose changing, and who decides` | 50 | `reporting.md` |
| `### How fresh the conversation's picture is, and how to refresh it` | 59 | `conversation.md` |
| `## How work flows once you approve it` | 69 | `work.md` |
| `### Letting the harness choose the work` | 100 | `work.md` |
| `### Reviewing what a branch adds up to` | 43 | `work.md` |
| `### Publishing, and the merge that follows it` | 58 | `work.md` |
| `## Configuring a project` | 90 | **merged** into `docs/configuration.md`'s index — see below |
| `### Running several Claude accounts` | 67 | **merged** with its parent — row added by yoyodyne-ifd.121.6 |
| `### Keeping the configuration out of the repository` | 85 | **merged** with its parent — row added by yoyodyne-ifd.121.6 |
| `## Artifact identity` | 82 | `artifacts.md` |
| `## Goals, and what work serves them` | 64 | `artifacts.md` |
| `## What a change upstream leaves stale` | 40 | `artifacts.md` |
| `## Architectural invariants` | 60 | `artifacts.md` |
| `## Operations and recovery` + all children except Slack | 454 | `operations.md` |
| `### Reporting into Slack` | 40 | `reporting.md` |
| `## Further reading` | 13 | **stays**, replaced by the index above |

`## Configuring a project` is the one section that merges rather than moves. It
is a narrative retelling of what `docs/configuration.md`'s own opening already
says, and keeping both is an invitation to drift that has no reader on its side.
The run that moves it reconciles the two into the configuration index and leaves
the README pointing at it.

## What the configuration guide becomes

`docs/configuration.md` **stays at that path**. It becomes a short index — what
a project configuration is, what owning it costs, and the map of the seven
documents — plus the Tier 1 redirect stubs. Keeping the path is not a
convenience: it is what makes the frozen anchors above possible at all.

**This table is stale, and by how much was measured on 2026-09-05.** It was
reconciled against `docs/configuration.md` on 2026-08-24 for yoyodyne-ifd.166,
when the file was 2,989 lines across 48 headings. It is now **3,848 lines across
57 headings** — 859 lines and nine headings the table has never seen, none of
which has a row:

- `## Keeping the configuration outside the repository` (`:317–365`)
- `### The release-readiness workflow` (`:1406–1478`)
- `### The environment a check runs in` (`:1592–1605`)
- `## Running a work item against the workflow definition` (`:1935–1959`) and
  its child `### The definition is the project's to own` (`:1960–2102`)
- `### Publishing from a fork` (`:2210–2244`) — a fourth child of
  `## Publishing through pull requests`, whose row names three
- `## Waiting out a network that dropped` (`:2535–2584`)
- `#### Crossing a cap the operator decides to cross` (`:3150–3215`)
- `### Pooling work across several accounts` (`:3370–3464`)

Four of the nine are cited from outside the guide —
`#the-release-readiness-workflow`,
`#running-a-work-item-against-the-workflow-definition`,
`#crossing-a-cap-the-operator-decides-to-cross`, and
`#pooling-work-across-several-accounts` — and the second of those is Tier 1,
frozen by `.yoyodyne/workflows/delivery.yaml`. So the configuration split cannot
treat this list as tidying: a tranche executing the table as it stands would drop
four sections other documents point at. **The whole table must be re-derived
before the next tranche executes against it**, and the counts and spans in it
below are the 2,989-line file's rather than the file as it now reads. The
destinations are still the right destinations; it is the enumeration that has
stopped being exhaustive, which is the third time in this document's life that
has been true and the reason every table here now carries the method that
produced it.

The bracketed span beside each count is that section's line range in
`configuration.md` as it stood at the 2026-08-24 reconciliation. Re-derive the
whole table by listing the file's `##` and `###` headings, ignoring the ones
inside fenced examples, and partitioning the file between them; a heading with no
row here means the file has moved on and the run that found it should say so
rather than choose a home.

| Current section | Lines | Destination |
|---|---|---|
| `# Yoyodyne configuration` (opening, what owning defaults costs) | 24 (1–24) | **stays** as the index opening, merged with the README's `## Configuring a project` |
| `## Creating a project configuration` | 36 (25–60) | `configuration/setup.md` |
| `### When the repository ignores the configuration` | 30 (61–90) | `configuration/setup.md` |
| `### Where the tracker syncs` | 31 (91–121) | `configuration/setup.md` |
| `## Layout` | 106 (122–227) | `configuration/setup.md` |
| `## Discovery` | 40 (228–267) | `configuration/setup.md` |
| `## Precedence` | 79 (268–346) | `configuration/setup.md` |
| `## Merge and removal semantics` (+ `### What fails closed`) | 92 (2518–2609) | `configuration/setup.md` |
| `## Extending a built-in bundle` (+ `### Converting an inheriting configuration…`) | 51 (2880–2930) | `configuration/setup.md` |
| `## Migrating from .yoyodyne.yaml` | 16 (2931–2946) | `configuration/setup.md` |
| `## Inspection` | 43 (2947–2989) | `configuration/setup.md` |
| `## Product specifications` (+ `### What the Lead Product Manager sees…`) | 122 (347–468) | `configuration/artifacts.md` |
| `## Artifact identity and metadata` | 113 (469–581) | `configuration/artifacts.md` |
| `### Approving a document` | 63 (582–644) | `configuration/artifacts.md` |
| `### Who may change an artifact` | 47 (765–811) | `configuration/artifacts.md` |
| `### Protected paths in a developer's change` | 113 (812–924) | `configuration/artifacts.md` |
| `### Proposing a change to a document you do not own` | 91 (925–1015) | `configuration/artifacts.md` |
| `### What reaches the queue` | 120 (645–764) | `configuration/goals.md` |
| `### Traceability: references and orphans` | 44 (1016–1059) | `configuration/goals.md` |
| `### Goals, and the work attributed to them` | 155 (1060–1214) | `configuration/goals.md` |
| `### What a change upstream leaves stale` | 55 (1215–1269) | `configuration/goals.md` |
| `## Architectural invariants` | 66 (1270–1335) | `configuration/goals.md` |
| `## Checks` (+ `### What init proposes`, `### How long a check may take`) | 155 (1336–1490) | `configuration/runs.md` |
| `## Scheduling ready work` (+ `### Watching instead of draining`, `### When a configuration change takes effect`, `### Why each run says why it was there`) | 188 (1491–1678) | `configuration/runs.md` |
| `## Publishing through pull requests` (+ its three children) | 185 (1679–1863) | `configuration/publishing.md` |
| `## Losing a race for the target branch` | 41 (2026–2066) | `configuration/publishing.md` |
| `## Waiting out a provider that refuses` | 116 (1864–1979) | `configuration/recovery.md` |
| `## Relaunching a run the provider killed` | 46 (1980–2025) | `configuration/recovery.md` |
| `## Triage thresholds` (+ `### What one work item has been given`) | 351 (2167–2517) | `configuration/recovery.md` |
| `## How long one role may ask another` | 40 (2067–2106) | `configuration/agents.md` |
| `## Research sources` | 60 (2107–2166) | `configuration/agents.md` |
| `## Provider accounts` | 38 (2610–2647) | `configuration/agents.md` |
| `## Operators` | 74 (2648–2721) | `configuration/agents.md` |
| `## Personas` | 43 (2837–2879) | `configuration/agents.md` |
| `## Reporting to Slack` (+ `### Avatars`) | 115 (2722–2836) | `configuration/agents.md` |

Five rows changed destination or arrived in this reconciliation, and each is a
judgement rather than arithmetic, so each is stated:

- **`### When the repository ignores the configuration`** is a child of
  `## Creating a project configuration` describing what `init` and
  `config validate` warn about. It goes where its parent goes.
- **`## Provider accounts`** and **`## Research sources`** go to
  `configuration/agents.md`: one names the account each agent runs under, the
  other names the commands a conversational role's questions are answered by.
  Both are configuration of the agents rather than of a run.
- **`## How long one role may ask another`** is the section that belonged to no
  document at all, and it goes to `configuration/agents.md` for the same reason:
  `exchange.max_rounds` bounds one agent asking another, which is a property of
  the agents rather than of the work. It was cited from `README.md:1487` and
  `docs/conversation.md:777`, so a tranche that dropped it would have left two
  live fragments resolving to nothing; ifd.160's trim deleted the README's copy
  of the section that made the first of those, so `docs/conversation.md:777` is
  now the only one, and the argument for the row is unchanged.
- **`## Merge and removal semantics`** (+ `### What fails closed`) moves from
  `configuration/publishing.md` to `configuration/setup.md`. It was misassigned
  by its title: its content is how `extends` layers combine and what refuses a
  configuration at load, not anything about a git merge. It belongs beside
  `## Precedence` and `## Extending a built-in bundle`, and moving it makes
  `configuration.md:224`'s link to `#what-fails-closed` an intra-document link
  rather than a cross-document one.

The index at `docs/configuration.md` carries, verbatim as headings so the slugs
survive:

```markdown
## Checks

Moved to [`configuration/runs.md`](configuration/runs.md#checks). This heading
stays so the link in every generated `.yoyodyne/config.yaml` keeps resolving.

## Product specifications

Moved to [`configuration/artifacts.md`](configuration/artifacts.md#product-specifications).
```

## What the split breaks that neither item mentions

**The Lead Product Manager stops being given the content that moves.**
`internal/contextbundle/product.go` carries this repository's own shipped
documentation as a named set — at drafting
`[]string{"README.md", "docs/configuration.md"}`, and now
`HarnessShippedDocumentation` — deliberately a named set rather than a walk, so
that a walk does not sweep the design document in. yoyodyne-ifd.312 scoped it to
this repository and made every other project's set
`product.shipped_documentation` in its own configuration, because the eight
paths are generic enough that an adopting project holds files at them meaning
something else; nothing about what this repository is given changed with it, and
the paragraphs below still describe the set this repository carries. After this
split those two files are an index and a landing
page, and everything the Lead Product Manager reads them *for* lives in thirteen
documents that set did not name. The README half of that has happened:
yoyodyne-ifd.160 trimmed the README and grew the set to eight in the same
change, so the six README-split documents are named. The seven
`docs/configuration/` documents are not, and will not need to be until the
configuration guide is actually split.

This is the same failure the comment above that variable already records: ifd.20
narrowed the Lead Product Manager's view, and the cost came due when it drafted a
work item that mis-assumed which surfaces existed. Doing it again by accident,
through a documentation restructure, would be worse than doing it on purpose.

So: **`HarnessShippedDocumentation` grows to name every document in this map's table**,
and that is part of the execution work rather than a follow-up. The run that
lands the last split document lands the list. **The six README-split documents
are now named**: yoyodyne-ifd.160 added `conversation.md`, `work.md`,
`reporting.md`, `artifacts.md`, `operations.md`, and `developing-yoyo.md` in the
same change that trimmed the README, because that trim is what would otherwise
have turned this from a pending edit into the ifd.20 failure repeated — the
Lead Product Manager reading a landing page and drafting work against surfaces it
could no longer see. The seven `docs/configuration/` documents are still
outstanding, and the tranche that lands each one adds it. The set stays
explicit — this map is the enumeration it needs, which is the argument for the
map existing as a checked-in document rather than as a decision recorded in a
conversation.

**One document in the table above is still not in the set, and it is not one of
the seven.** [`docs/provider-plugins.md`](provider-plugins.md) has a row in
[what each document is for](#what-each-document-is-for), is linked from the
README's index, and describes a surface the product ships — declaring a provider
of your own — but `HarnessShippedDocumentation` does not name it, and did not before the
split either. Read strictly, "grows to name every document in this map's table"
says it should. yoyodyne-ifd.121.6 found this and did not act on it: what the
Lead Product Manager is shown is a change to the product's behaviour rather than to
the split's structure, and it is the Lead Product Manager's own view that would
change. It is recorded here so the decision is taken rather than inherited.

**Nothing mechanically enforces that a fragment resolves.** yoyodyne-ifd.121.2
makes "every link resolves" its definition of done, and the only thing standing
behind that today is a reviewer reading a diff — which is exactly the check that
does not catch a silently-ignored fragment. This has already happened once at a
much smaller scale: yoyodyne-ifd.54 records two README anchors left dead by a
rename, found by hand afterwards and repaired by asking a later run to grep for
the old names. This effort moves 22 README sections and 34 configuration
sections at once, and 91 intra-document links with them. yoyodyne-ifd.166 is
the second demonstration and a nearer miss: four configuration sections had no
row in the table a tranche executes against, three of them cited from documents
that would have been left pointing at nothing.

**That checker now exists.** `internal/doclink` resolves both relative paths and
fragments across every Markdown file in the repository — `.github/` included,
skipping only `.git`, `.dolt`, `testdata`, and `dist` — and
`TestThisRepositoryOwnDocumentationLinksResolve` runs it over this checkout
under `make test`, which is one of this project's declared checks. So a moved
anchor that resolves to nothing fails a gate on every run and every repair,
rather than costing a reviewer a paragraph saying they could not verify it. The
policy above is enforced rather than asked for, and the remaining manual part is
only the tables' *line numbers*, which the checker has no opinion about.
`make check` carries it too, since `check` is `fmtcheck test race vet` and
`test` is what runs it. yoyodyne-ifd.310 was admitted to arrange exactly that
and found it already done; [the diagnosis](diagnoses/yoyodyne-ifd-310-link-checker-already-in-the-gate.md)
records how that was established, and why the `scripts/check-doc-links.py` the
reports named is on three preserved branches rather than on main.

**One kind of citation the checker cannot see**, named here so no later run
mistakes a green `make test` for a clean sweep:

- **Anchors cited from the tracker.** `.beads/issues.jsonl` carries citations in
  item descriptions and recorded review findings, and the export is not the
  store, so nothing a change to this repository does can repair one. Today
  yoyodyne-ifd.117.1 — open — cites `README.md#talking-to-the-other-agents`,
  which ifd.160 deleted.

yoyodyne-ifd.121.6's sweep named two others, and both are covered now:

- **Anchors cited from files that are not Markdown** are read. Go, YAML, and
  shell under the root are swept for a `path.md#anchor` citation, `.yoyodyne/`
  included, so `.yoyodyne/workflows/delivery.yaml:8` citing
  `docs/configuration.md#running-a-work-item-against-the-workflow-definition`,
  and the Go comments and struct literals citing `docs/configuration.md#checks`
  and `#provider-accounts`, each fail `make test` when the heading moves.
- **A link written as a URL into this repository's own forge home** is resolved
  against the checkout, which is how `.github/release-notes-preamble.md:28`
  reaches `README.md#getting-started` and why that Tier 1 anchor is enforced
  rather than only recorded (yoyodyne-ifd.315).

The one that remains is why the sweep is still stated as a method rather than
replaced by the test. Re-derive with `README.md#` and `configuration.md#` over
the whole tree including `.beads/issues.jsonl`, and separately over the protected
homes: the test says whether a citation resolves, never whether an anchor is
frozen, and the tracker is past what any check here can reach.

**And the walkthrough is a gate now too.** `.github/workflows/ci.yml` runs
`make getting-started` on every pull request, which executes the README's
"Getting started" against a throwaway project rather than reading it. The link
checker says a fragment resolves; the walkthrough says the steps still work. The
README split needed both, and until yoyodyne-ifd.121.6 the second one ran only
when somebody remembered.

## What the architect is being asked

1. **Is the Tier 1 / Tier 2 / Tier 3 rule the right shape** — freeze by who
   cites, rewrite everything internal, and refuse to leave a dangling fragment?
   The alternative considered and rejected was freezing every currently-cited
   anchor, which buys a stub graveyard in exchange for never re-deriving a
   citation list.
2. **Does a mention in a closed item's review transcript freeze an anchor?**
   yoyodyne-ifd.121.6's sweep says no, and [says why](#the-moved-anchor-policy):
   almost every tracker mention this document records lives in an item's recorded
   review findings rather than in its description, so the axis that decides
   anything is whether the item is still work somebody will act on. An item
   nobody will act on again cannot be made unfollowable, so freezing on its
   transcript buys the stub graveyard question 1 rejected, by another door.
   It is not academic: read the other way, all six of the items mentioning a
   README anchor in [the README table](#citations-of-readmemd-anchors) freeze
   one, and the configuration split then owes the README a redirect stub for
   `#keeping-the-configuration-out-of-the-repository`, which everything in
   [what the README becomes](#what-the-readme-becomes) is written against. The
   distinction is a developer's, drawn to resolve a review finding, and it is the
   kind of thing the tier policy's owner should settle rather than inherit.
3. **Should the frozen anchors live in `docs/configuration.md` as stubs, or
   should `docs/configuration.md` keep the real `## Checks` content** and the
   split take everything else? The stub is proposed because a document that
   keeps one arbitrary section for URL reasons is harder to explain than a
   document that keeps none.
4. **Is `docs/configuration/` as a directory beside a surviving
   `docs/configuration.md` acceptable**, or should the split targets be flat
   `docs/configuration-runs.md` and so on? The directory is proposed; the
   coexistence is admittedly odd to look at.
5. **Does `HarnessShippedDocumentation` growing to fifteen entries change what the
   Lead Product Manager should be given at all**, or is the enumeration the right
   answer to keep?

## What the operator is being asked

Whether the README landing at roughly 500 lines rather than a short landing page
is the right trade — it is the direct consequence of keeping install and the
three getting-started steps in the README, which the adoption goal's own wording
demands. Everything else in this map follows from decisions already made in
yoyodyne-ifd.121 and yoyodyne-ifd.117.
