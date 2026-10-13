#!/usr/bin/env bash
#
# cut-release.sh - cut one release: gate on its notes, on the system matching
# what it records about itself, on the adoption walkthrough and on the checks,
# build the archives and their checksums for the tag, then tag the commit they
# were built from -- which is the commit origin's default branch already holds.
#
#   make release VERSION=v0.3.0      what an operator runs
#   scripts/cut-release.sh v0.3.0    the same thing, without make
#
# This is a verb rather than a checklist because the cadence is daily. A
# checklist is a list of things somebody can skip on the day they are in a
# hurry, and the day somebody is in a hurry is exactly the day the walkthrough
# would have caught something. One invocation runs the gate every time, and a
# red gate refuses the cut and names what was red.
#
# What it does not do is publish. The tag push is the irreversible half and it
# is what .github/workflows/release.yml acts on, so it stays a separate act the
# operator takes deliberately; this prints the one command. Everything before
# that point is what this makes certain.
#
# A cut writes nothing to the default branch. On 2026-09-20 the v0.5.0 cut
# passed every gate, committed its own housekeeping on main -- the readiness
# result stamped into the notes, and the tracker's derived exports -- and
# tagged that commit; the push it printed was refused, because main requires a
# pull request, and a pull request produces a different commit, so the tag
# named a tree main would never hold, and a second cut had fresh housekeeping
# it could not push either. So the tag names the commit origin's default branch
# holds when the gates pass, which is what the gates ran on; the tracker's
# derived exports are excluded from the tree check and left where they lie,
# since nothing a release ships is built from them; and the readiness result
# reaches the notes the way every other change reaches the default branch,
# through a pull request ahead of the tag. Whether the branch is protected is
# asked before anything is built, and answered with which step it changes.
#
# The order is deliberate: every gate runs before anything is written, and the
# tag is created last, so a refusal at any point leaves the checkout exactly
# as it was. Two refusals leave something behind, each of them ahead of the
# walkthrough and the cross-compile, and each says outright what it wrote. A
# release whose notes are missing has them drafted -- a cut with no story to
# tell writes docs/releases/<tag>.md from the work items that landed and
# refuses, so the tag lands on a commit that carries its own notes. And a
# release whose notes do not carry a current readiness result has it committed
# on a branch, `release/<tag>-readiness-<commit>`, pushed, and a pull request
# opened for it where the forge's command line is installed; the cut after
# that merge finds the result on the default branch and goes through.
#
# The harness runs this too, as the gates of a cut it makes itself under
# approvals.releases: automatic, with YOYODYNE_RELEASE_CUT=automatic in the
# environment (internal/releasecut). That mode prepares nothing and tags
# nothing, because preparing the notes and stamping them is the release item's
# reviewed run and the tag, its push, and the record of both are the harness's.
# So in it: notes that are missing, or a readiness result that is not current,
# refuse with exit status 3 and write nothing -- no draft, no branch, no pull
# request -- which is the harness's cue to hand the release back to the Lead
# Product Manager; a checkout on a detached HEAD is accepted, because the
# harness cuts in a worktree of its own at the commit origin's default branch
# holds, and that commit is still required to be what origin holds; origin
# being unreachable refuses rather than being named as unchecked, because
# HEAD at origin's default branch is a gate the harness cannot skip; and once
# every gate is green it prints the commit they ran on and exits 0 with no tag
# written.
#
# Requires git 1.9 or newer, for the `:(exclude)` pathspec the cleanliness
# check uses. Also make, go, python3 -- which compares and stamps the readiness
# result here as well as rendering the draft in scripts/release-notes.sh --
# and bd, which the release-readiness gate reads the work items through; the
# notes read them from the tracker's export instead. gh, the forge's command
# line, is optional: with it the cut asks whether the default branch is
# protected and opens the readiness pull request itself; without it, both are
# named as unchecked and the branch is named to open one from. Nothing outside
# the repository is written except that branch and its pull request, and the
# tag is never pushed.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
walkthrough="$repository/scripts/walk-adoption.sh"
notes_writer="$repository/scripts/release-notes.sh"
# Where a release's notes live, versioned beside the code they describe and
# named for the tag, so the release page and the repository tell one story.
notes_home="$repository/docs/releases"
# When this was reached through `make release`, use the same make.
make_program="${MAKE:-make}"
# The forge's command line, which asks whether the default branch is protected
# and opens the readiness pull request. Optional: without it both are named as
# unchecked. Overridable the way make is, so the verb's own suite can hand it
# a stub and a machine with none.
gh_program="${GH:-gh}"
# Where `dist` writes, spelled the way the Makefile spells it: DIST ?= dist,
# and a caller who overrode it on the make command line has it in the
# environment here too.
dist_directory="$repository/${DIST:-dist}"
# What a release tag is: vMAJOR.MINOR.PATCH, and nothing else. The release
# workflow triggers on "v*" and the archives are named for this string, so a
# tag in another shape produces a release whose files nobody can predict. The
# narrowness is deliberate -- `git describe` output is a well-formed semver
# prerelease, and a looser pattern would let VERSION's build-time default be
# cut as if it were a release.
tag_pattern='^v[0-9]+\.[0-9]+\.[0-9]+$'
# The tracker's derived exports. Beads keeps the issues in a local database and
# writes these out as a passive dump, so they change whenever anything touches
# the tracker -- including the adoption walkthrough this gate runs itself, and
# a harness running beside the cut, which rewrites them continuously. The
# archives a release ships are not built from them, and the notes are drafted
# from the issues export and committed before the cut, so they are excluded
# from the tree check and never committed: a cut that committed them would be a
# commit the default branch cannot take. Each one is also a change a run
# declares the primary checkout may acquire while it works, in
# internal/cli/run.go, and internal/cli/release_repository_test.go holds this
# list inside that one.
derived_exports=(".beads/interactions.jsonl" ".beads/issues.jsonl")
# Where the release-readiness section lives inside a release's notes, spelled
# the way internal/cli/conformance.go renders it. The cut replaces what is
# between these two lines and leaves everything the product manager wrote around
# them alone, so stamping a result twice leaves one section rather than two.
readiness_begin="<!-- yoyodyne:release-readiness -->"
readiness_end="<!-- /yoyodyne:release-readiness -->"

step()   { printf '\n=== %s\n' "$*"; }
# Every refusal names the tag it did not cut, because the operator's next
# question is always whether anything was left behind. Nothing was, unless the
# refusal says so.
refuse() { printf '\ncut-release: %s\n' "$*" >&2; exit 1; }
# Whether the harness is running this as the gates of its own cut; see the
# header. Anything else is a person's cut, exactly as before.
automatic=0
[ "${YOYODYNE_RELEASE_CUT:-}" = "automatic" ] && automatic=1
# A refusal over the release's preparation, in the harness's mode: the notes
# or their readiness result are the release item's reviewed run to land, so
# the harness hands the release back rather than preparing it itself. Its own
# exit status is how the harness tells it from a red gate.
refuse_preparation() { printf '\ncut-release: %s\n' "$*" >&2; exit 3; }

walk_log=""
readiness=""
stamped=""
index=""
cleanup() {
  [ -z "$walk_log" ] || rm -f "$walk_log"
  [ -z "$readiness" ] || rm -f "$readiness"
  [ -z "$stamped" ] || rm -f "$stamped"
  [ -z "$index" ] || rm -f "$index"
}
trap cleanup EXIT

git -C "$repository" rev-parse --git-dir >/dev/null 2>&1 ||
  refuse "$repository is not a git repository"

latest="$(git -C "$repository" tag --list 'v*' --sort=-v:refname | head -1)"
[ -n "$latest" ] || latest="(none yet)"

tag="${1:-${VERSION:-}}"
if [ -z "$tag" ]; then
  refuse "no version given. Pass the tag: make release VERSION=v0.3.0 (latest tag: $latest)"
fi
if ! [[ $tag =~ $tag_pattern ]]; then
  refuse "\"$tag\" is not a release tag; a release tag is vMAJOR.MINOR.PATCH. Pass one: make release VERSION=v0.3.0 (latest tag: $latest)"
fi
if git -C "$repository" rev-parse -q --verify "refs/tags/$tag" >/dev/null; then
  refuse "$tag already exists. A release is cut once; pick the next tag (latest tag: $latest)"
fi

step "the commit $tag would name"

# The archives are built from the working tree and the tag is placed on HEAD,
# so the two agree only while the tree is clean. A dirty tree would ship a
# binary built from something no commit holds.
#
# The tracker's derived exports are not that: nothing is built from them, and
# under a daily cadence they are dirty nearly every day, so refusing on them
# stalls the cut on a day nobody is standing there to stash. They are left
# where they lie -- see the header -- so what this asks is that the tree is
# clean of everything else, and it still names what it found.
exclude_exports=()
for export_path in "${derived_exports[@]}"; do
  exclude_exports+=(":(exclude)$export_path")
done
dirty="$(git -C "$repository" status --porcelain -- . "${exclude_exports[@]}")"
if [ -n "$dirty" ]; then
  printf '%s\n' "$dirty" >&2
  refuse "the working tree has uncommitted changes, so the archives would not be the commit $tag names. Commit or stash them first"
fi
if [ -n "$(git -C "$repository" status --porcelain -- "${derived_exports[@]}")" ]; then
  printf "the tracker's derived exports have changed; nothing a release ships is built from them, and they are left where they lie\n"
fi

# Releases come off the branch integration lands on. A tag on a feature branch
# builds and publishes perfectly well and names work that is not in the
# product, which is the expensive mistake this cadence could make daily.
default_branch="$(git -C "$repository" symbolic-ref --quiet --short refs/remotes/origin/HEAD 2>/dev/null || true)"
default_branch="${default_branch#origin/}"
[ -n "$default_branch" ] || default_branch="main"
branch="$(git -C "$repository" branch --show-current)"
# The harness's worktree is detached at the commit origin's default branch
# holds, which the check below still requires; a branch other than the default
# is refused in either mode.
if [ "$branch" != "$default_branch" ] && { [ "$automatic" != "1" ] || [ -n "$branch" ]; }; then
  refuse "a release is cut from $default_branch; this checkout is on \"${branch:-a detached HEAD}\""
fi

head="$(git -C "$repository" rev-parse HEAD)"
printf 'HEAD: %s (%s)\n' "$head" "$default_branch"

# Whether HEAD is what the rest of the world has needs the network. Where it is
# reachable this is settled; where it is not, it is named as unchecked rather
# than passed over, the same way the walkthrough treats a claim it cannot
# exercise. A silent skip here reads as "today's work" and can be yesterday's.
origin_reachable=0
if git -C "$repository" fetch --quiet origin "$default_branch" 2>/dev/null; then
  remote="$(git -C "$repository" rev-parse FETCH_HEAD)"
  if [ "$head" != "$remote" ]; then
    refuse "HEAD is not where origin/$default_branch is ($remote), so $tag would name a commit the product does not have. Pull or push first"
  fi
  printf 'origin/%s agrees\n' "$default_branch"
  origin_reachable=1
elif [ "$automatic" = "1" ]; then
  refuse "origin is unreachable, so whether HEAD is where origin/$default_branch is could not be checked, and the harness cuts nothing it has not checked"
else
  printf 'SKIPPED: origin is unreachable, so whether HEAD is current was not checked\n'
fi

step "whether $default_branch is protected"
# Asked here, before anything is built, because the v0.5.0 cut found out at the
# very end: every gate green, the archives built, the tag placed, and the push
# refused. The cut writes nothing to the default branch whichever way this
# answers, so protection cannot refuse it any more; what it changes is what
# stands between a cut and its tag, and that is said here rather than found
# out. The forge is asked through its command line, both ways it can protect a
# branch -- the older per-branch protection, and a ruleset -- and a question it
# cannot answer is named as unchecked rather than guessed at.
#
# Per-branch protection is read off the branch itself, the 'protected' field of
# repos/{owner}/{repo}/branches/<branch>, as the harness's own forge check reads
# it (internal/publish/github.go). The branch's protection endpoint needs
# administrator rights, and the forge answers an account without them with the
# same 404 it gives an unprotected branch, so asking it first read a protected
# branch as open to anybody but an administrator. It is still asked, once the
# branch is known to be protected, and only to say which rules apply.
probe_protection() {
  if [ "$origin_reachable" != "1" ]; then
    printf 'unchecked: origin is unreachable'
    return
  fi
  if ! command -v "$gh_program" >/dev/null 2>&1; then
    printf 'unchecked: %s is not installed' "$gh_program"
    return
  fi
  local answer
  if ! answer="$(cd "$repository" && "$gh_program" api "repos/{owner}/{repo}/branches/$default_branch" --jq '.protected' 2>&1)"; then
    printf 'unchecked: %s could not ask the forge: %s' "$gh_program" "$(printf '%s' "$answer" | head -1)"
    return
  fi
  case "$answer" in
    true)
      local applied
      if applied="$(cd "$repository" && "$gh_program" api "repos/{owner}/{repo}/branches/$default_branch/protection" \
          --jq '[(if .required_pull_request_reviews then "required pull request reviews" else empty end),
                 (if .required_status_checks then "required status checks" else empty end),
                 (if .restrictions then "push restrictions" else empty end),
                 (if .required_linear_history.enabled then "linear history" else empty end),
                 (if .enforce_admins.enabled then "enforced for administrators" else empty end)] | join(", ")' 2>/dev/null)" \
          && [ -n "$applied" ]; then
        printf 'protected: by branch protection, with %s' "$applied"
      else
        printf 'protected: by branch protection, whose rules this account cannot read'
      fi
      return
      ;;
    false) ;;
    *) printf 'unchecked: %s could not ask the forge: its answer did not say whether %s is protected' "$gh_program" "$default_branch"; return ;;
  esac
  local rules
  if ! rules="$(cd "$repository" && "$gh_program" api "repos/{owner}/{repo}/rules/branches/$default_branch" \
      --jq '[.[] | select(.type == "pull_request" or .type == "required_status_checks" or .type == "update")] | length' 2>&1)"; then
    printf 'unchecked: %s could not ask the forge: %s' "$gh_program" "$(printf '%s' "$rules" | head -1)"
    return
  fi
  if [ "$rules" != "0" ]; then printf 'protected: by a ruleset'; else printf 'open'; fi
}
protection="$(probe_protection)"
case "$protection" in
  protected*)
    printf 'origin/%s is protected (%s): a change reaches it only through a pull request. The cut\n' "$default_branch" "${protection#protected: }"
    printf 'writes nothing to it either way, so what changes is what stands between a cut and\n'
    printf 'its tag: a readiness result not yet in docs/releases/%s.md reaches it only through\n' "$tag"
    printf 'the pull request this cut opens and stops at, and publishing is the tag alone.\n'
    ;;
  open)
    printf 'origin/%s is not protected, and the cut writes nothing to it all the same. A readiness\n' "$default_branch"
    printf 'result not yet in docs/releases/%s.md is committed on a branch, which may be merged\n' "$tag"
    printf 'through its pull request or pushed to %s directly, and publishing is the tag alone.\n' "$default_branch"
    ;;
  *)
    printf 'SKIPPED: whether origin/%s is protected was not checked (%s). The cut writes\n' "$default_branch" "${protection#unchecked: }"
    printf 'nothing to it either way: a readiness result not yet in docs/releases/%s.md goes\n' "$tag"
    printf 'through a pull request, and publishing is the tag alone.\n'
    ;;
esac

step "gate: this release's notes"
# A release nobody can read is a release nobody adopts, so the notes are a gate
# rather than a courtesy -- and they are gated here, before the walkthrough and
# the cross-compile, because drafting them costs seconds and those cost minutes.
#
# The notes are the one thing a cut cannot finish on its own: which work is key
# functionality, which is an enhancement, and which critical fix belongs at the
# top is the product manager's judgement. So a missing file is drafted from the
# items that landed and the cut refuses, leaving the operator a file to read,
# place, and commit. The tag then names a commit that carries its own notes.
notes_file="$notes_home/$tag.md"
if [ ! -f "$notes_file" ] && [ "$automatic" = "1" ]; then
  refuse_preparation "docs/releases/$tag.md is not on $default_branch, so $tag was not cut and nothing was written. The release item's reviewed run drafts and curates the notes; the harness does not draft them"
fi
if [ ! -f "$notes_file" ]; then
  printf '%s has no notes yet, so they are drafted here from what landed since\n' "$tag"
  printf 'the last tag. Nothing else has been written and no tag exists.\n\n'
  if ! bash "$notes_writer" "$tag"; then
    refuse "$tag's notes could not be drafted, so $tag was not cut"
  fi
  refuse "drafted docs/releases/$tag.md and stopped there. Read it, move each item into the section its work belongs in, commit it, then cut $tag again -- that is the only thing this left behind"
fi
printf 'docs/releases/%s.md is present and committed, so %s will name a commit\n' "$tag" "$tag"
printf 'that carries its own notes\n'

step "gate: release readiness"
# A tag says the system matches what it records about itself, not only that its
# tests pass. This runs the release-readiness workflow -- a sequence in the
# project-owned definition format rather than Go control flow -- over the
# checkout: the canonical artifacts and their references, the links the
# documentation makes to itself, the architectural invariants, and every admitted
# work item's attribution to a goal the product states. Staleness is surveyed
# alongside and refuses nothing, because a cut that failed over an amendment
# would teach an operator not to amend.
#
# It is rendered as the Markdown section the notes carry rather than as the
# operator's reading, and there is one invocation rather than two: a gate read
# one way and a notes section written from a second run would be two results,
# and only one of them would be the one that refused or did not.
printf 'The tag is refused unless the system still matches what it records about\n'
printf 'itself. The section below is what this tag carries in its notes.\n\n'
if ! "$make_program" -C "$repository" build >/dev/null; then
  refuse "the harness could not be built, so release readiness was never checked and $tag was not cut"
fi
readiness="$(mktemp "${TMPDIR:-/tmp}/cut-release-readiness.XXXXXX")"
# Run from the repository rather than from wherever the operator is standing:
# the harness discovers a project's configuration by walking up from the working
# directory, and a cut invoked from elsewhere would otherwise check whichever
# project that directory happens to be inside, or none.
if (cd "$repository" && ./bin/yoyo conformance --notes) > "$readiness"; then
  cat "$readiness"
else
  cat "$readiness" >&2
  refuse "release readiness is red, so $tag was not cut and nothing was written"
fi

step "gate: the readiness result is in this tag's notes on $default_branch"
# The result is stamped into docs/releases/<tag>.md so the notes the tag names
# say what was true of the tree they describe -- and the tag names a commit on
# the default branch, so the stamp has to be on the default branch before the
# tag exists. It gets there the way every other change does, through a pull
# request, which means a cut that finds it missing stops here and the cut after
# the merge goes through. That is gated before the walkthrough and the
# cross-compile for the same reason the notes are: stopping costs seconds here
# and minutes there.
#
# "Current" is the verdict and the pinned definition, not the whole text. The
# counts in a reading move with the tracker every day, and a harness running
# beside the cut moves them between the merge and the next cut; a stamp held to
# the whole text would never be current and the loop would never close. What
# the notes record is the reading the first cut took; what the tag certifies is
# that a second reading, on the tree it names, ended the same way.
#
# The stamp is committed with plumbing, on top of the commit origin holds, so
# the checkout is not touched: no branch is checked out, no hook fires, and the
# working tree is exactly as it was. The branch is named for the tag and the
# commit it was stamped against, so two cuts at the same commit find the same
# branch and the second says so rather than pushing over the first.
[ ! -L "$notes_file" ] ||
  refuse "$notes_file is a symbolic link, so stamping $tag's readiness result would write outside the repository"
stamped="$(mktemp "${TMPDIR:-/tmp}/cut-release-stamped.XXXXXX")"
if YOYODYNE_READINESS_BEGIN="$readiness_begin" YOYODYNE_READINESS_END="$readiness_end" \
     python3 - "$notes_file" "$readiness" "$stamped" <<'PY'
import os
import re
import sys

begin, end = os.environ["YOYODYNE_READINESS_BEGIN"], os.environ["YOYODYNE_READINESS_END"]
notes_path, section_path, stamped_path = sys.argv[1], sys.argv[2], sys.argv[3]

with open(notes_path, encoding="utf-8") as handle:
    notes = handle.read()
with open(section_path, encoding="utf-8") as handle:
    section = handle.read().strip("\n")


def recorded(text):
    """What a readiness section records: the verdict the workflow ended in, and
    the definition it was pinned to. Both are lines internal/cli/conformance.go
    renders; a section carrying neither is somebody's hand edit, and is replaced."""
    ended = re.search(r"ended in \*\*([^*]+)\*\*", text)
    pinned = re.search(r"Pinned to `([^`]+)`", text)
    return (ended.group(1) if ended else None, pinned.group(1) if pinned else None)


reading = recorded(section)
if reading[0] is None:
    sys.exit("the readiness section carries no verdict line this can compare, so whether the notes are current cannot be judged")

start = notes.find(begin)
if start == -1:
    reason = "the notes carry no readiness result"
    updated = notes.rstrip("\n") + "\n\n" + section + "\n"
else:
    stop = notes.find(end, start)
    if stop == -1:
        sys.exit("the notes open a release-readiness section and never close it; repair it by hand and cut again")
    stamp = recorded(notes[start:stop + len(end)])
    if stamp == reading:
        print("current: the notes record that the workflow ended in **%s**%s, which is what this reading found"
              % (reading[0], "" if reading[1] is None else " pinned to `%s`" % reading[1]))
        sys.exit(0)
    if stamp[0] is None:
        reason = "the notes carry a readiness section with no verdict in it"
    elif stamp[0] != reading[0]:
        reason = "the notes record that the workflow ended in **%s** and this reading ended in **%s**" % (stamp[0], reading[0])
    else:
        def pinned(pin):
            return "pinned to nothing" if pin is None else "pinned to `%s`" % pin
        reason = "the notes record a reading %s and this one is %s" % (pinned(stamp[1]), pinned(reading[1]))
    updated = notes[:start] + section + notes[stop + len(end):]

with open(stamped_path, "w", encoding="utf-8") as handle:
    handle.write(updated)
print("stale: %s" % reason)
sys.exit(3)
PY
then
  stamp_current=1
else
  case $? in
    3) stamp_current=0 ;;
    *) refuse "$tag's readiness result could not be compared with $notes_file, so $tag was not cut" ;;
  esac
fi

if [ "$stamp_current" != "1" ] && [ "$automatic" = "1" ]; then
  refuse_preparation "docs/releases/$tag.md on $default_branch does not carry a readiness result matching this reading, so $tag was not cut and nothing was written. The release item's reviewed run stamps it with yoyo conformance --notes; the harness opens no pull request for it"
fi
if [ "$stamp_current" != "1" ]; then
  # The branch carries the tag and the commit the stamp was taken against, so a
  # second cut at the same commit -- the operator running it again before the
  # merge -- finds the branch rather than pushing over it, and a cut at a later
  # commit, after a merged stamp went stale, names a branch of its own.
  stamp_branch="release/$tag-readiness-$(git -C "$repository" rev-parse --short "$head")"
  if git -C "$repository" rev-parse -q --verify "refs/heads/$stamp_branch" >/dev/null ||
     { [ "$origin_reachable" = "1" ] &&
       [ -n "$(git -C "$repository" ls-remote --heads origin "refs/heads/$stamp_branch")" ]; }; then
    refuse "$tag's readiness result is already on the branch $stamp_branch, waiting to reach $default_branch. Merge its pull request, then cut $tag again; nothing was written"
  fi
  # A tree that is HEAD's with the stamped notes in place of the committed
  # ones, made in an index of its own so the checkout's is untouched.
  index="$(mktemp "${TMPDIR:-/tmp}/cut-release-index.XXXXXX")"
  blob="$(git -C "$repository" hash-object -w --path "docs/releases/$tag.md" "$stamped")"
  GIT_INDEX_FILE="$index" git -C "$repository" read-tree "$head"
  GIT_INDEX_FILE="$index" git -C "$repository" update-index --add --cacheinfo "100644,$blob,docs/releases/$tag.md"
  tree="$(GIT_INDEX_FILE="$index" git -C "$repository" write-tree)"
  stamp_commit="$(git -C "$repository" commit-tree "$tree" -p "$head" -m "$tag: record the release-readiness result in its notes")"
  git -C "$repository" update-ref "refs/heads/$stamp_branch" "$stamp_commit" ""
  printf 'committed docs/releases/%s.md on %s (%s), on top of the commit origin/%s holds\n' \
    "$tag" "$stamp_branch" "$stamp_commit" "$default_branch"
  if [ "$origin_reachable" != "1" ]; then
    refuse "$tag's readiness result is not in its notes on origin/$default_branch, and origin is unreachable, so it was committed on the local branch $stamp_branch and the cut stopped there. Push that branch, open a pull request for it, merge it, then cut $tag again -- that branch is the only thing this left behind"
  fi
  if ! git -C "$repository" push -q origin "refs/heads/$stamp_branch:refs/heads/$stamp_branch"; then
    refuse "$tag's readiness result was committed on the local branch $stamp_branch and could not be pushed, so the cut stopped there. Push that branch, open a pull request for it, merge it, then cut $tag again -- that branch is the only thing this left behind"
  fi
  printf 'pushed %s to origin\n' "$stamp_branch"
  if command -v "$gh_program" >/dev/null 2>&1; then
    if pull_request="$(cd "$repository" && "$gh_program" pr create --base "$default_branch" --head "$stamp_branch" \
        --title "$tag: record the release-readiness result in its notes" \
        --body "$(printf 'The release-readiness result for %s, stamped into docs/releases/%s.md by the cut, ahead of the tag. Once this is on %s the next cut of %s goes through.' "$tag" "$tag" "$default_branch" "$tag")" 2>&1)"; then
      printf 'opened %s\n' "$(printf '%s' "$pull_request" | tail -1)"
      refuse "$tag's readiness result is not in its notes on origin/$default_branch yet, so it was committed on $stamp_branch and a pull request opened for it, and the cut stopped there. Merge it, then cut $tag again -- that branch and its pull request are the only things this left behind"
    fi
    printf '%s\n' "$pull_request" >&2
    refuse "$tag's readiness result was committed on $stamp_branch and pushed, and the pull request for it could not be opened, so the cut stopped there. Open one for $stamp_branch against $default_branch, merge it, then cut $tag again -- that branch is the only thing this left behind"
  fi
  refuse "$tag's readiness result is not in its notes on origin/$default_branch yet, so it was committed on $stamp_branch and pushed, and the cut stopped there. $gh_program is not installed, so open the pull request for $stamp_branch against $default_branch yourself, merge it, then cut $tag again -- that branch is the only thing this left behind"
fi
printf 'docs/releases/%s.md carries this reading, so %s names a commit whose notes say what was true of it\n' "$tag" "$tag"

step "gate: the adoption walkthrough"
printf 'A release is what the install path consumes, so the documented first hour\n'
printf 'is walked before the tag exists rather than after it is published.\n\n'
walk_log="$(mktemp "${TMPDIR:-/tmp}/cut-release-walk.XXXXXX")"
if ! bash "$walkthrough" 2>&1 | tee "$walk_log"; then
  named="$(grep 'FAIL:' "$walk_log" || true)"
  if [ -n "$named" ]; then
    printf '\nwhat the walkthrough found:\n%s\n' "$named" >&2
  else
    printf '\nthe walkthrough did not finish:\n%s\n' "$(tail -5 "$walk_log")" >&2
  fi
  refuse "the adoption walkthrough is red, so $tag was not cut and nothing was written"
fi

step "gate: make check"
# The release workflow runs this after the tag push. Running it here means a
# red check costs a rerun rather than a pushed tag with no release behind it.
if ! "$make_program" -C "$repository" check; then
  refuse "make check is red, so $tag was not cut and nothing was written"
fi

step "build: the archives and their checksums for $tag"
if ! "$make_program" -C "$repository" dist-verify VERSION="$tag"; then
  refuse "the release build for $tag failed, so no tag was written"
fi

step "tag"
# The gates ran on HEAD, and HEAD is where origin's default branch was when
# they started; the tag names that commit and nothing newer. Integration lands
# on the branch continuously, so by now origin may hold more -- that is fine,
# the commit the gates ran on is still in its history, and it is said rather
# than raced after. What is not fine is the branch having been rewritten
# underneath the cut, which would leave the tag naming a commit the product no
# longer has; that is refused. Where origin is unreachable the tag is placed on
# what the gates ran on and the check is named as unchecked, as above.
if [ "$origin_reachable" = "1" ]; then
  if git -C "$repository" fetch --quiet origin "$default_branch" 2>/dev/null; then
    remote="$(git -C "$repository" rev-parse FETCH_HEAD)"
    if [ "$head" != "$remote" ]; then
      if git -C "$repository" merge-base --is-ancestor "$head" "$remote"; then
        printf 'origin/%s has moved on to %s since the gates started; %s names the commit\n' "$default_branch" "$remote" "$tag"
        printf 'they ran on, which %s still holds\n' "$default_branch"
      else
        refuse "origin/$default_branch has been rewritten since the gates started ($remote no longer holds $head), so $tag would name a commit the product does not have. Cut again from the current branch"
      fi
    fi
  else
    printf 'SKIPPED: origin became unreachable, so whether %s still holds %s was not checked\n' "$default_branch" "$head"
  fi
fi
# The harness's mode stops here: the tag, its push, and the record of both are
# the harness's, made against the commit this names (see the header).
if [ "$automatic" = "1" ]; then
  printf '\n=== gates green\n'
  printf 'checked commit: %s\n' "$head"
  exit 0
fi
# Last, and only now: everything above passed, so this tag never needs undoing.
# On the commit the gates ran on, by hash, so a tree the walkthrough left dirty
# in the tracker's exports changes nothing about what the tag names.
git -C "$repository" tag -a "$tag" -m "$tag" "$head"
printf '%s tagged at %s\n' "$tag" "$head"

printf '\n=== cut\n'
# The cut is finished from here: the tag exists and the archives are on disk.
# Nothing below is allowed to fail it. A completed release that exits non-zero
# because a report could not be printed is the worst of both -- the tag is
# real, `make release` says it failed, and the push it needs never appears.
cat "$dist_directory/checksums.txt" 2>/dev/null ||
  printf 'the cut succeeded, but %s/checksums.txt is not where it was expected\n' "$dist_directory"
# The tag alone. It names a commit origin already holds, so there is no branch
# to carry with it, and a branch push is what a protected default branch
# refuses.
printf '\nPublishing is the tag push, which the release workflow acts on:\n'
printf '  git push origin %s\n' "$tag"
