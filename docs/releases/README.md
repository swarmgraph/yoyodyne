# Release notes

*One file per release tag, named for it. Part of
[yoyo's documentation](../../README.md#further-reading).*

`v0.3.1.md` is what changed in `v0.3.1`. The file is written before the tag
exists, so the release page and the repository tell one story rather than two:
the [release workflow](../../.github/workflows/release.yml) publishes this file as
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
to `v0.5.0` has its notes before it exists.

[`v0.6.0.md`](v0.6.0.md) is the other exception: it names a tag that was never
cut. Its notes landed on `main`, no `v0.6.0` tag followed, and everything they
describe shipped in `v0.7.0`, whose notes, [`v0.7.0.md`](v0.7.0.md), carry it
and cover everything from `v0.5.0`. The file stays as the record of what was
prepared, and says at its top that it was never tagged.

From [`v0.7.0.md`](v0.7.0.md) on, a release's notes exist before its tag but
are not at the commit the tag names. They cover everything merged into `main`
up to a commit they name, in the words "up to and including commit `<the
commit's whole name>`", so that work landing while they wait in review cannot
make them wrong, and the tag is cut at that commit, which the notes landed
after. The harness's cut reads the commit from the notes on `main` and refuses
notes that name none, or one `main` does not hold
([cutting a decided release](../operations.md#cutting-a-decided-release)).
The release workflow finds no notes in the tagged commit's tree, so it reads
them from `main` and publishes them as the page's text, taking them only where
they name the tagged commit. To see the body a page will carry, from a
checkout that has fetched `main`:

```sh
bash scripts/release-body.sh v0.7.0 body.md      # these notes, install instructions under them
```

GitHub runs a tag's workflow and scripts as they are at the tagged commit. A
release tagged at a commit from before the workflow read notes from `main`
publishes the install instructions alone, and the harness cannot write the
text of a forge release. For such a tag, whoever cuts it puts the notes on the
page afterwards, from a checkout of `main` that carries them:

```sh
bash scripts/release-body.sh v0.7.0 body.md
gh release edit v0.7.0 --notes-file body.md
```

`v0.7.0`'s notes name a commit from before the release workflow began reading
notes from `main`, so a tag at that commit needs the manual step above. Naming
a later commit in the notes, one that has that workflow, makes the step
unnecessary; which commit v0.7.0 names is for its own release item to decide.
