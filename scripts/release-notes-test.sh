#!/usr/bin/env bash
#
# release-notes-test.sh - exercise scripts/release-notes.sh against a fabricated
# repository and a fabricated tracker export, so what the notes say about a
# range is executed rather than asserted.
#
#   bash scripts/release-notes-test.sh
#
# The script's whole job is a join: which work items landed between two tags,
# and what the tracker's export says about each. Running it against this
# repository would mean its real history and its real export, so the answer
# would change every day and the assertions could only be about shape. Each case
# builds its own instead -- a scratch repository holding a copy of the script,
# commits whose subjects name work items the way this project's do, and a
# `.beads/issues.jsonl` written from fixtures in the shape the tracker exports.
# A stub `bd` on PATH refuses everything, so the claim that the script never
# asks the tracker itself is executed too.
#
# Two things are worth testing here. The placement, because it is the part with
# a rule in it: a feature opens, a task sits under it, a bug sinks, and a
# critical bug rises back to the top. And the shape of an entry, because the
# operator fixed it: the description under every item, and a line naming the
# persona that asked for an item that did not come from a person -- one case
# holds the three origins side by side, so each shape is seen against the other
# two. Everything else the file says is the tracker's words carried through.
#
# scripts/release-body.sh is covered here too, against the notes these cases
# just wrote. It is what the release workflow runs on a tag push, so the only
# other place it would ever execute is a real publication. The body it composes
# is held to being the notes and the preamble and nothing else, and its bound
# on the body's length -- a warning at a fraction of what the forge accepts, a
# refusal over it -- is driven with the bounds set small enough to reach.
#
# Everything lives under one temporary root that is removed on exit. The real
# tracker is never read and nothing outside that root is written.
#
# Requires bash, git, and python3.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/release-notes-test.XXXXXX")"
scratch="$(cd "$scratch" && pwd)"
project="$scratch/product"
failures=0

trap 'rm -rf "$scratch"' EXIT

# The scratch repository is committed and tagged into, and a machine with no git
# identity configured would fail at that rather than at the claim.
export GIT_AUTHOR_NAME="release-notes-test" GIT_AUTHOR_EMAIL="test@example.invalid"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
# The script reads VERSION from the environment when no argument is given, so a
# caller's own VERSION would silently supply the tag one case withholds.
unset VERSION

step()   { printf '\n=== %s\n' "$*"; }
pass()   { printf '  ok: %s\n' "$*"; }
fail()   { printf '  FAIL: %s\n' "$*"; failures=$((failures + 1)); }

contains() {
  case "$1" in (*"$2"*) pass "$3" ;; (*) fail "$3 -- got: $1" ;; esac
}
missing() {
  case "$1" in (*"$2"*) fail "$3 -- got: $1" ;; (*) pass "$3" ;; esac
}

# item writes one work item into the scratch export, in the shape the tracker
# exports it: one JSON object per line, as `bd show <id> --json` would answer
# for that item. The goal is written where the harness writes it, onto the
# notes, because that is where the script reads it from. Status defaults to
# closed, which is what a release's notes are made of; a case that wants
# unfinished work passes its own.
#
# The seventh argument is where the item came from, written onto the notes the
# way the harness writes an admission (internal/chat/tracker.go): `operator` is
# the ordinary admission by the product manager, with a reason naming the
# operator; `report` is that admission plus the line naming the collected report
# it was admitted from and the role that filed it; `decomposition` is the
# development manager carving the item out of a parent. The eighth is the
# description.
item() {
  mkdir -p "$project/.beads"
  python3 - "$project/.beads/issues.jsonl" "$1" "$2" "$3" "$4" "${5:-}" "${6:-closed}" "${7:-operator}" "${8:-}" <<'PY'
import json
import sys

path, identifier, title, kind, priority, served, status, origin, description = sys.argv[1:10]
if origin == "decomposition":
    notes = ("Created under scratch-ifd.9, decomposing it by the development manager "
             "in conversation chat-419cedb4, after turn 80.\n\n"
             "Reason: the epic's first buildable slice, kept its own run so the review stays readable.")
else:
    notes = ("Admitted to the backlog by the product manager in conversation chat-91253e0e, "
             "after turn 408.\n\nReason: Operator request: the harness should run until told to stop.")
if served:
    notes += "\n\nGoal served: %s" % served
if origin == "report":
    notes += ('\n\nAdmitted from report report-6f1a2b3c, filed at "warning" by the reviewer: '
              "the goals listing prints in tracker order, which is not the order a reader needs.")
with open(path, "a", encoding="utf-8") as export:
    export.write(json.dumps({
        "_type": "issue",
        "id": identifier,
        "title": title,
        "description": description,
        "issue_type": kind,
        "priority": int(priority),
        "status": status,
        "notes": notes,
        "metadata": {},
    }) + "\n")
PY
}

mkdir -p "$scratch/bin"
cat > "$scratch/bin/bd" <<'SH'
#!/usr/bin/env bash
# The tracker itself, which the notes writer must never ask: it reads the export
# the tracker leaves beside its store. An invocation here is the script's header
# claim failing, so it fails loudly rather than answering.
echo "bd was invoked: the notes writer is supposed to read .beads/issues.jsonl" >&2
exit 1
SH
chmod +x "$scratch/bin/bd"
export PATH="$scratch/bin:$PATH"

# The three origins the entry shape has to show side by side: an item the
# operator asked for through the product manager, one admitted from a
# reviewer's report, and one the development manager decomposed out of a parent.
item scratch-ifd.1 "Watch mode: the harness runs until told to stop" feature 1 \
  "Let configurable agents run unattended." closed operator \
  "Operator request, 2026-08-20: the harness should keep choosing work until told to stop, rather than draining the queue once and exiting."
item scratch-ifd.2 "Format the goals listing for reading" task 2 "" closed report \
  "The goals listing prints in tracker order. Print it in the order the goals document keeps.

Done means the listing and the document agree,
and a test pins the order."
item scratch-ifd.3 "A completed run is discarded after its terminal result" bug 0 \
  "Never lose finished work." closed operator \
  "A run that reached its terminal result was cleaned up before the result was recorded, so the work it finished was discarded."
item scratch-ifd.4 "Shift-return inserts a newline in the conversation input" bug 2 "" \
  closed decomposition \
  "Shift-return submits the turn instead of inserting a newline, so a multi-line message cannot be typed."
item scratch-ifd.5 "The thing that shipped in the last release" feature 1
# The item a commit names without the item being done: a parent epic is named by
# every child's commit, and a multi-part item by each part as it lands.
item scratch-ifd.6 "The epic every child's commit names" task 1 "" in_progress

mkdir -p "$project/scripts"
cp "$repository/scripts/release-notes.sh" "$project/scripts/release-notes.sh"
chmod +x "$project/scripts/release-notes.sh"
git init -q "$project"
# git init's default branch name varies by version; name it once so nothing here
# depends on which git ran.
git -C "$project" symbolic-ref HEAD refs/heads/main
git -C "$project" add -A
git -C "$project" commit -qm "the first commit"

# land writes one commit whose subject names a work item, the way this project's
# integration commits do.
land() { git -C "$project" commit -q --allow-empty -m "product: $1 $2"; }

land scratch-ifd.5 "The thing that shipped in the last release"
git -C "$project" tag -a v0.1.0 -m v0.1.0

land scratch-ifd.1 "Watch mode: the harness runs until told to stop"
# A hyphenated ordinary word in a subject looks exactly like an id from a
# regular expression's side, and the export is what tells them apart.
land scratch-ifd.2 "Format the goals listing, including the well-known ordering"
land scratch-ifd.3 "A completed run is discarded after its terminal result"
land scratch-ifd.4 "Shift-return inserts a newline in the conversation input"
land scratch-ifd.6 "The epic every child's commit names"

draft() { ( cd "$project" && ./scripts/release-notes.sh "$@" 2>&1 ) || true; }

# section_of reports the heading one line sits under, which is the whole of what
# "the sections follow the operator's shape" means for a single item.
section_of() {
  awk -v needle="$2" '
    /^## / { heading = substr($0, 4); next }
    index($0, needle) { print heading; exit }
  ' "$1"
}

# entry_of prints one item's whole entry: its title line and every indented or
# blank line under it, up to the next entry or heading. What the fixed shape
# says about one item is a claim about exactly these lines.
entry_of() {
  awk -v needle="(\`$2\`)" '
    index($0, needle) && /^- / { inside = 1; print; next }
    inside && (/^- / || /^## /) { exit }
    inside { print }
  ' "$1"
}

notes="$project/docs/releases/v0.2.0.md"

step "the notes are written from the items that landed in the range"
output="$(draft v0.2.0)"
contains "$output" "wrote $notes" "says where it wrote"
contains "$output" "from 4 closed work item(s)" "counts what landed"
contains "$output" "across v0.1.0..HEAD" "names the range it walked"
contains "$output" "looked like work item ids and are not in the tracker export" \
  "a hyphenated word the export does not know is named rather than silently dropped"
missing "$output" "bd was invoked" "and the tracker itself was never asked"
if [ -f "$notes" ]; then
  pass "the file is named for the tag, in the notes home"
else
  fail "no $notes was written"
fi

body="$(cat "$notes" 2>/dev/null || true)"
contains "$body" "# v0.2.0" "the file is titled with the tag"
contains "$body" "covering v0.1.0 to v0.2.0" "and says what it covers"
contains "$body" '(`scratch-ifd.1`)' "each item carries its id"
contains "$body" "Serves: Let configurable agents run unattended." "and the goal it served"
missing "$body" "scratch-ifd.5" "an item that landed before the previous tag is not in it"

step "every entry carries the item's description under its title"
entry="$(entry_of "$notes" scratch-ifd.1)"
contains "$entry" "  Operator request, 2026-08-20: the harness should keep choosing work" \
  "the description is under the title, indented into the entry"
entry="$(entry_of "$notes" scratch-ifd.2)"
contains "$entry" "  The goals listing prints in tracker order. Print it in the order the goals document keeps." \
  "a description's first paragraph is one line"
contains "$entry" "  Done means the listing and the document agree, and a test pins the order." \
  "a newline inside a paragraph is folded, so its shape does not depend on how it was typed"
contains "$entry" "keeps.

  Done means" "and a blank line between paragraphs is kept as the break it was"

step "an item that did not come from a person says which persona asked for it, and why"
# The three shapes, side by side: the operator's item says nothing about who
# asked, because the notes record the product manager's admission and not
# which person spoke to her; the other two name the persona and its reason.
entry="$(entry_of "$notes" scratch-ifd.1)"
missing "$entry" "Requested by" "an item the operator asked for through the product manager carries no line"
entry="$(entry_of "$notes" scratch-ifd.2)"
contains "$entry" "  Requested by the reviewer: the goals listing prints in tracker order, which is not the order a reader needs." \
  "an item admitted from a reviewer's report names the reviewer, in the report's own words"
entry="$(entry_of "$notes" scratch-ifd.4)"
contains "$entry" '  Requested by the development manager, decomposing `scratch-ifd.9`: the epic'"'"'s first buildable slice, kept its own run so the review stays readable.' \
  "an item the development manager decomposed names him, the parent, and his reason"
lines="$(printf '%s\n' "$entry" | grep -c '^  Requested by' || true)"
if [ "$lines" = "1" ]; then
  pass "and it is one line"
else
  fail "expected one Requested by line, got $lines"
fi
# The entry's shape in full: title, goal, requester, description -- in that
# order, so the two short attribution lines sit under the title and the long
# description is set apart below them.
expected="$(printf '%s\n' \
  '- **Shift-return inserts a newline in the conversation input** (`scratch-ifd.4`)' \
  '  Requested by the development manager, decomposing `scratch-ifd.9`: the epic'"'"'s first buildable slice, kept its own run so the review stays readable.' \
  '' \
  '  Shift-return submits the turn instead of inserting a newline, so a multi-line message cannot be typed.')"
if [ "$entry" = "$expected" ]; then
  pass "an entry is exactly the fixed shape"
else
  fail "the entry differs from the fixed shape -- got: $entry"
fi

step "only what the tracker calls closed is published as shipped"
contains "$output" "1 work item(s) the commits named are not closed" \
  "an item a commit named and the tracker has not closed is counted"
missing "$body" "The epic every child's commit names" "and is not in the notes"
missing "$body" "scratch-ifd.6" "not even as an id"

step "each item starts in the section its type puts it in"
for probe in "Watch mode|Key functionality" \
             "Format the goals listing|Enhancements" \
             "A completed run is discarded|Key functionality" \
             "Shift-return inserts|Bug fixes"; do
  needle="${probe%%|*}"
  want="${probe##*|}"
  got="$(section_of "$notes" "$needle")"
  if [ "$got" = "$want" ]; then
    pass "\"$needle\" is under $want"
  else
    fail "\"$needle\" is under \"${got:-no heading}\", expected $want"
  fi
done
printf '  (the third is the one exception in the shape: a critical bug fix goes up\n'
printf '   with key functionality rather than to the bottom)\n'

step "the sections are in the operator's order"
order="$(grep '^## ' "$notes" | tr '\n' '|')"
if [ "$order" = "## Key functionality|## Enhancements|## Bug fixes|" ]; then
  pass "key functionality, then enhancements, then bug fixes"
else
  fail "the sections came out as: $order"
fi

step "the release page's body is the tag's own notes with the preamble under them"
# scripts/release-body.sh is what the release workflow runs on a tag push. It is
# exercised here because the alternative is finding out what it does during a
# real publication, which is the one run where being wrong is expensive.
mkdir -p "$project/.github"
printf '## Install\n\nDownload the archive for your platform below.\n' \
  > "$project/.github/release-notes-preamble.md"
cp "$repository/scripts/release-body.sh" "$project/scripts/release-body.sh"
composed="$scratch/body.md"
if output="$( ( cd "$project" && bash scripts/release-body.sh v0.2.0 "$composed" ) 2>&1 )"; then
  pass "composing a body for a tag that has notes succeeds"
else
  fail "release-body.sh failed for a tag with notes -- got: $output"
fi
body="$(cat "$composed" 2>/dev/null || true)"
contains "$body" "Watch mode" "the tag's own notes are the body"
contains "$body" "## Key functionality" "with their sections intact"
contains "$body" "## Install" "and the install preamble under them"
missing "$body" "# v0.2.0" "the file's title is dropped, because the page already carries it"
# Byte for byte: the notes under their title, one blank line, the preamble, and
# nothing else. The forge's generated changelog is what v0.5.0's page carried
# besides these and what the forge refused it for, and that is the workflow's
# flag rather than this script's -- internal/cli's release test holds the
# workflow to passing the notes file alone -- but "nothing else" is a claim
# about this composition, so it is compared whole rather than probed for parts.
{ tail -n +2 "$notes"; printf '\n'; cat "$project/.github/release-notes-preamble.md"; } \
  > "$scratch/expected-body.md"
if cmp -s "$composed" "$scratch/expected-body.md"; then
  pass "the body is the curated notes with the preamble under them and nothing else"
else
  fail "the body differs from the notes and the preamble -- got: $body"
fi
missing "$output" "warning" "and a body this size is nothing to warn about"

step "the body is warned about at a configured fraction of the forge's limit, and refused over it"
# The forge refuses a body over a fixed length, and the notes grow with the
# backlog, so the composition measures what it composed. The bounds are read
# from the environment for exactly this: the fabricated notes are a few hundred
# bytes, and the claim is about the fraction and the limit rather than about
# 125,000 characters.
length="$(wc -c < "$composed" | tr -d ' ')"
if output="$( ( cd "$project" && RELEASE_BODY_LIMIT=$((length + 10)) bash scripts/release-body.sh v0.2.0 "$composed" ) 2>&1 )"; then
  pass "a body past the fraction but under the limit still composes"
else
  fail "release-body.sh refused a body under the limit -- got: $output"
fi
contains "$output" "release-body: warning: the body for v0.2.0 is $length bytes, at or past 75% of the $((length + 10)) characters the forge accepts" \
  "and is warned about, naming the length, the fraction, and the limit"
if cmp -s "$composed" "$scratch/expected-body.md"; then
  pass "with the body written whole all the same"
else
  fail "the warned-about body differs from the notes and the preamble"
fi
output="$( ( cd "$project" && RELEASE_BODY_LIMIT=$((length * 2)) bash scripts/release-body.sh v0.2.0 "$composed" ) 2>&1 )" || true
missing "$output" "warning" "a body under the fraction of a larger limit is not warned about"
output="$( ( cd "$project" && RELEASE_BODY_LIMIT=$((length * 2)) RELEASE_BODY_WARN_PERCENT=40 bash scripts/release-body.sh v0.2.0 "$composed" ) 2>&1 )" || true
contains "$output" "at or past 40% of the $((length * 2)) characters" "until the fraction is configured lower"
if output="$( ( cd "$project" && RELEASE_BODY_LIMIT=$((length - 1)) bash scripts/release-body.sh v0.2.0 "$composed" ) 2>&1 )"; then
  fail "release-body.sh composed a body over the limit -- got: $output"
else
  pass "a body over the limit is refused rather than handed to the forge to refuse"
fi
contains "$output" "release-body: the body for v0.2.0 is $length bytes, and the forge refuses a release body over $((length - 1)) characters" \
  "naming the length and the limit"
contains "$output" "shorten $notes" "and the file to shorten"
output="$( ( cd "$project" && RELEASE_BODY_LIMIT=many bash scripts/release-body.sh v0.2.0 "$composed" ) 2>&1 )" || true
contains "$output" "RELEASE_BODY_LIMIT is 'many', which is not a number" "a limit that is not a number is refused before anything is measured"

step "a tag with no notes publishes the preamble rather than failing the workflow"
if output="$( ( cd "$project" && bash scripts/release-body.sh v9.9.9 "$composed" ) 2>&1 )"; then
  pass "composing a body for a tag with no notes file still succeeds"
else
  fail "release-body.sh failed for a tag with no notes -- got: $output"
fi
contains "$output" "publishes the preamble alone" "and says so rather than doing it quietly"
body="$(cat "$composed" 2>/dev/null || true)"
contains "$body" "## Install" "the preamble is the whole body"
missing "$body" "Watch mode" "and no other release's notes leaked into it"
if cmp -s "$composed" "$project/.github/release-notes-preamble.md"; then
  pass "byte for byte"
else
  fail "the body for a tag with no notes is not exactly the preamble -- got: $body"
fi

step "a tagged commit with no notes takes them from the default branch, where they name it"
# A release is tagged at the commit its notes name and the notes land on the
# default branch afterwards, so the tagged tree has none. notes_on commits a
# notes file onto a branch of its own with plumbing, leaving the checkout and
# its index alone, the way the default branch gains them after the tag.
notes_on() {
  local branch="$1" tag="$2" named="$3" index blob tree commit
  printf '# %s\n\nEverything up to and including commit `%s`.\n\n## Key functionality\n\n- **Landed later** (`scratch-ifd.9`)\n' "$tag" "$named" \
    > "$scratch/later-notes.md"
  index="$scratch/later-index"
  blob="$(git -C "$project" hash-object -w "$scratch/later-notes.md")"
  GIT_INDEX_FILE="$index" git -C "$project" read-tree HEAD
  GIT_INDEX_FILE="$index" git -C "$project" update-index --add --cacheinfo "100644,$blob,docs/releases/$tag.md"
  tree="$(GIT_INDEX_FILE="$index" git -C "$project" write-tree)"
  commit="$(git -C "$project" commit-tree "$tree" -p HEAD -m "the notes for $tag land after the commit they name")"
  git -C "$project" update-ref "refs/heads/$branch" "$commit"
  rm -f "$index"
}
tagged="$(git -C "$project" rev-parse HEAD)"
git -C "$project" tag -a v0.4.0 -m v0.4.0 "$tagged"
notes_on later-main v0.4.0 "$tagged"
if output="$( ( cd "$project" && RELEASE_NOTES_FROM=later-main bash scripts/release-body.sh v0.4.0 "$composed" ) 2>&1 )"; then
  pass "composing a body from notes on the default branch succeeds"
else
  fail "release-body.sh failed for notes on the default branch -- got: $output"
fi
body="$(cat "$composed" 2>/dev/null || true)"
contains "$body" "Landed later" "the notes on the default branch are the body"
contains "$body" "## Install" "with the install preamble under them"
missing "$body" "# v0.4.0" "the file's title is dropped there too"
contains "$output" "read from later-main" "and it says where the notes came from"

notes_on other-main v0.4.0 "$(git -C "$project" rev-parse 'v0.1.0^{commit}')"
output="$( ( cd "$project" && RELEASE_NOTES_FROM=other-main bash scripts/release-body.sh v0.4.0 "$composed" ) 2>&1 )" || true
contains "$output" "not $tagged, which v0.4.0 names" "notes naming another commit are not taken as this tag's"
if cmp -s "$composed" "$project/.github/release-notes-preamble.md"; then
  pass "and the body is the preamble alone"
else
  fail "notes naming another commit reached the body -- got: $(cat "$composed")"
fi

# The tree at the tag may hold an earlier draft of the same notes, landed before
# the commit this one names; the notes on the default branch replace it.
printf '# v0.4.0\n\nEverything up to and including commit `%s`.\n\n- **Earlier draft**\n' \
  "$(git -C "$project" rev-parse 'v0.1.0^{commit}')" > "$project/docs/releases/v0.4.0.md"
output="$( ( cd "$project" && RELEASE_NOTES_FROM=later-main bash scripts/release-body.sh v0.4.0 "$composed" ) 2>&1 )" || true
body="$(cat "$composed" 2>/dev/null || true)"
contains "$body" "Landed later" "notes on the default branch that name the tag replace an earlier draft in the tagged tree"
missing "$body" "Earlier draft" "and none of the earlier draft is published"
missing "$output" "warning: $project/docs/releases" "with no warning about it"
output="$( ( cd "$project" && RELEASE_NOTES_FROM=other-main bash scripts/release-body.sh v0.4.0 "$composed" ) 2>&1 )" || true
contains "$(cat "$composed" 2>/dev/null || true)" "Earlier draft" "a draft nothing replaces is still published"
contains "$output" "published as they stand" "with a warning that it names another commit"
printf '# v0.4.0\n\n- **No commit named**\n' > "$project/docs/releases/v0.4.0.md"
output="$( ( cd "$project" && RELEASE_NOTES_FROM=later-main bash scripts/release-body.sh v0.4.0 "$composed" ) 2>&1 )" || true
contains "$(cat "$composed" 2>/dev/null || true)" "No commit named" "notes in the tree that name no commit stay the tag's own"
rm -f "$project/docs/releases/v0.4.0.md"

notes_on untagged-main v0.5.0 "$tagged"
output="$( ( cd "$project" && RELEASE_NOTES_FROM=untagged-main bash scripts/release-body.sh v0.5.0 "$composed" ) 2>&1 )" || true
contains "$output" "no tag v0.5.0 here" "notes for a tag that does not exist here are not matched against anything"
if cmp -s "$composed" "$project/.github/release-notes-preamble.md"; then
  pass "and the body is the preamble alone"
else
  fail "notes for an absent tag reached the body -- got: $(cat "$composed")"
fi
# The cases below draft from the last tag, so this case's tag and branches go.
git -C "$project" tag -d v0.4.0 >/dev/null
git -C "$project" branch -q -D later-main other-main untagged-main

step "notes are written once, and then edited"
output="$(draft v0.2.0)"
contains "$output" "already exists" "refuses to overwrite notes that are there"
contains "$output" "--force" "and names the way to mean it"

step "--print writes nothing"
output="$(draft v0.3.0 --print)"
contains "$output" "# v0.3.0" "prints the draft"
if [ ! -f "$project/docs/releases/v0.3.0.md" ]; then
  pass "and left no file behind"
else
  fail "--print wrote docs/releases/v0.3.0.md"
fi

step "--help describes the entry shape and writes nothing"
output="$(draft --help)"
contains "$output" "Requested by the <persona>: <why>" "the help shows the fixed entry shape"
contains "$output" "its description" "and says the description is in it"
missing "$output" "no version given" "and asks for no tag"

step "the tag has to be a release tag, and one has to be given"
output="$(draft 0.2.0)"
contains "$output" "is not a release tag" "refuses a tag with no leading v"
output="$(draft v0.2.0-143-gf5e427a)"
contains "$output" "is not a release tag" "refuses \`git describe\` output"
output="$(draft)"
contains "$output" "no version given" "refuses with no tag at all"

step "a range with nothing in it is refused rather than published as empty notes"
# A release nobody can say anything about is worth a person's attention, and
# empty notes on a release page are worse than notes somebody had to write.
git -C "$project" tag -a v0.2.0 -m v0.2.0
output="$(draft v0.3.0)"
contains "$output" "nothing to write" "says there is nothing to write from"
contains "$output" "v0.2.0..HEAD" "and names the range it found empty"

step "a range holding only unfinished work is refused too"
# Different from the range above and worth telling apart: work did land here,
# and none of it is something a release page may claim shipped.
land scratch-ifd.6 "The epic every child's commit names"
output="$(draft v0.3.0)"
contains "$output" "none of them is closed" "says the work is not closed rather than that nothing landed"

step "a checkout without the tracker's export is refused, naming it"
# Diagnosed apart from an empty range: an export that is not there and nothing
# having landed look the same from the notes' side and need opposite fixes.
mv "$project/.beads/issues.jsonl" "$scratch/issues.jsonl.aside"
output="$(draft v0.3.0 --print)"
contains "$output" ".beads/issues.jsonl is not here" "names the export it could not find"
mv "$scratch/issues.jsonl.aside" "$project/.beads/issues.jsonl"

step "an export with a torn line is refused rather than read short"
# A line the export writer left unfinished may be the item that landed; notes
# short by an item nobody was told about are worse than a draft that stopped.
printf '{"_type":"issue","id":"scratch-ifd.7","title":"a torn rec\n' >> "$project/.beads/issues.jsonl"
output="$(draft v0.3.0 --print)"
contains "$output" "cannot be read as a work item" "says the export could not be read"
contains "$output" "line 7" "and names the line"

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'release-notes.sh writes what landed, in the shape the operator asked for\n'
else
  printf '%d claim(s) did not hold\n' "$failures"
fi
exit "$failures"
