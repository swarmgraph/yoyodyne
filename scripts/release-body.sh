#!/usr/bin/env bash
#
# release-body.sh - compose one release page's body: the notes this repository
# carries for the tag, with the install preamble under them.
#
#   bash scripts/release-body.sh v0.3.1 /tmp/body.md
#
# This is the release workflow's own step, extracted so it is a thing that can
# be run and tested rather than eight lines of YAML whose first execution is a
# tag push against a real published release. .github/workflows/release.yml calls
# it and passes the result to `gh release create --notes-file`, as the whole of
# the body: the forge's own generated changelog is never appended to it.
#
# The composition is deliberate in two places. The tag's notes go first and the
# preamble under them, because what changed is what a returning reader came for
# and installing is what a new one came for, and only one of those two can be at
# the top. And the notes file's own `# <tag>` heading is dropped, because the
# release page is already titled with the tag and the same words twice reads
# like a mistake.
#
# A tagged commit whose tree has no notes takes them from the default branch
# where they landed afterwards, as long as they name that commit; that is how
# every release the harness cuts is published, since it tags the commit the
# notes name. A tag with notes in neither place publishes the preamble alone
# rather than failing. A
# release with thin notes is worth more than a pushed tag with no release behind
# it, and the tags cut before the notes home existed are exactly the ones that
# would otherwise turn a rerun of this workflow into a red build.
#
# The body is the whole of what the release page carries as text, and the forge
# refuses a body over a fixed length: 125,000 characters at GitHub, which
# v0.5.0's tag push met as `HTTP 422: Validation Failed: body is too long`. That
# body was these notes with the forge's own generated changelog under them,
# which the workflow no longer asks for; what is left is the curated notes, and
# they grow with the backlog. So the composed body is measured. At a fraction
# of the limit it is warned about on stderr, where the workflow's log shows it,
# so the notes are shortened before the release they would refuse; over the
# limit it is refused here, naming the length, rather than handed to the forge
# to refuse with less said. Both figures are read from the environment --
# RELEASE_BODY_LIMIT and RELEASE_BODY_WARN_PERCENT -- so the suite can exercise
# them against notes a few hundred bytes long.
#
# Requires bash and git. Nothing in the repository is written: the only thing
# written is the destination the caller named, and a temporary copy of notes
# read from the default branch, removed on exit.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
preamble="$repository/.github/release-notes-preamble.md"
notes_home="$repository/docs/releases"
limit="${RELEASE_BODY_LIMIT:-125000}"
warn_percent="${RELEASE_BODY_WARN_PERCENT:-75}"

refuse() { printf '\nrelease-body: %s\n' "$*" >&2; exit 1; }

tag="${1:-}"
destination="${2:-}"
[ -n "$tag" ] ||
  refuse "no tag given. Usage: bash scripts/release-body.sh <tag> <destination>"
[ -n "$destination" ] ||
  refuse "no destination given. Usage: bash scripts/release-body.sh <tag> <destination>"
case "$limit" in
  (''|*[!0-9]*) refuse "RELEASE_BODY_LIMIT is '$limit', which is not a number of characters" ;;
esac
case "$warn_percent" in
  (''|*[!0-9]*) refuse "RELEASE_BODY_WARN_PERCENT is '$warn_percent', which is not a percentage" ;;
esac
# The preamble is how a reader installs what they just downloaded, so a release
# published without it is worse than one this refused to compose.
[ -f "$preamble" ] || refuse "$preamble is missing, so there is no install section to publish"

notes="$notes_home/$tag.md"
# A release covers everything up to a commit its notes name and is tagged at
# that commit, and the notes land on the default branch after it, so the
# tagged commit's own tree usually has no notes. Then they are read from the
# default branch -- RELEASE_NOTES_FROM, origin/main unless the workflow names
# another -- and used only where they name the commit the tag names, in the
# words docs/releases/README.md gives: "up to and including commit `<sha>`".
notes_from="${RELEASE_NOTES_FROM:-origin/main}"
fetched=""
trap '[ -z "$fetched" ] || rm -f "$fetched"' EXIT
source=""
if [ -f "$notes" ]; then
  source="$notes"
else
  tagged="$(git -C "$repository" rev-parse -q --verify "refs/tags/$tag^{commit}" 2>/dev/null || true)"
  fetched="$(mktemp "${TMPDIR:-/tmp}/release-body-notes.XXXXXX")"
  if [ -z "$tagged" ]; then
    printf 'no %s, and no tag %s here to match notes on %s against\n' "$notes" "$tag" "$notes_from" >&2
  elif git -C "$repository" show "$notes_from:docs/releases/$tag.md" > "$fetched" 2>/dev/null; then
    named="$(grep -oE 'up to and including commit `[0-9a-f]{40}([0-9a-f]{24})?`' "$fetched" | head -1 | grep -oE '[0-9a-f]{40,64}' || true)"
    if [ "$named" = "$tagged" ]; then
      printf 'the notes for %s are read from %s, where they landed after %s\n' "$tag" "$notes_from" "$tagged" >&2
      source="$fetched"
      notes="$notes_from:docs/releases/$tag.md"
    elif [ -z "$named" ]; then
      printf 'docs/releases/%s.md on %s names no commit, so it is not taken as the notes of %s, which names %s\n' "$tag" "$notes_from" "$tag" "$tagged" >&2
    else
      printf 'docs/releases/%s.md on %s names %s, not %s, which %s names, so it is not taken as its notes\n' "$tag" "$notes_from" "$named" "$tagged" "$tag" >&2
    fi
  else
    printf 'docs/releases/%s.md is not on %s either\n' "$tag" "$notes_from" >&2
  fi
fi
: > "$destination"

if [ -n "$source" ]; then
  # Drop a leading `# <anything>` and keep everything else. Done by reading the
  # first line rather than with sed's addressed-block syntax, which BSD and GNU
  # sed disagree about often enough that a release page is the wrong place to
  # find out which one the runner has.
  first=""
  IFS= read -r first < "$source" || true
  case "$first" in
    ("# "*) tail -n +2 "$source" >> "$destination" ;;
    (*)     cat "$source" >> "$destination" ;;
  esac
  printf '\n' >> "$destination"
else
  printf 'no %s, so this release publishes the preamble alone\n' "$notes" >&2
fi

cat "$preamble" >> "$destination"

# Measured in bytes rather than characters. The forge counts characters, a byte
# count is never smaller than a character count, so this errs toward warning
# early on a body with anything outside ASCII in it -- and `wc -c` reads the
# same on every runner, where `wc -m` reads whatever the locale says. Compared
# at the hundredth so the percentage stays an integer, which is all the shell
# arithmetic there is.
length="$(wc -c < "$destination" | tr -d ' ')"
if [ "$length" -gt "$limit" ]; then
  refuse "the body for $tag is $length bytes, and the forge refuses a release body over $limit characters; shorten $notes before publishing, because the forge will not accept this one"
fi
if [ $((length * 100)) -ge $((limit * warn_percent)) ]; then
  printf 'release-body: warning: the body for %s is %s bytes, at or past %s%% of the %s characters the forge accepts; shorten the notes before a release is refused for length\n' \
    "$tag" "$length" "$warn_percent" "$limit" >&2
fi
