#!/usr/bin/env bash
#
# cut-release-test.sh - exercise scripts/cut-release.sh against fabricated
# repositories, so every refusal it can make is executed rather than asserted.
#
#   scripts/cut-release-test.sh
#
# The verb's whole value is that it refuses, so the refusals are what needs
# testing, and testing them against this repository would mean a real
# walkthrough and a real cross-compile for each one. What cut-release.sh needs
# is a git repository with a walkthrough, a notes writer, and a Makefile beside
# it, so each case builds one: a scratch repository holding a copy of the
# script, a stub walkthrough that is green or red on request, a stub notes
# writer, a stub forge command line that answers whether the default branch is
# protected and records the pull request it was asked to open, and a stub
# Makefile whose `check` and `dist-verify` targets do the same. Everything the
# script does to a repository -- reading it, drafting a release's notes into
# it, committing a readiness result on a branch of it, tagging it -- then
# happens to the scratch one.
#
# The real scripts/release-notes.sh is stubbed rather than copied, because what
# cut-release.sh is gated on is a file being there and a writer that can say it
# failed; what that writer puts in the file is
# scripts/release-notes-test.sh's claim.
#
# A protected default branch is a bare repository with a receive hook that
# refuses every push to main, the way a forge's protection does, unless the
# forge itself is merging a pull request -- which this stands in for by
# touching a file the hook looks for. The loop the verb is built around is
# driven through it: a cut that commits the readiness result on a branch and
# stops, the merge, and the cut that tags what main then holds, through to the
# tag push the verb prints, executed against that same remote.
#
# Everything lives under one temporary root that is removed on exit. No tag is
# written anywhere but there, and nothing is pushed anywhere but there.
#
# Requires bash, git, make, and python3.

set -euo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
scratch="$(mktemp -d "${TMPDIR:-/tmp}/cut-release-test.XXXXXX")"
failures=0

trap 'rm -rf "$scratch"' EXIT

# The scratch repositories are committed and tagged into, and a machine with no
# git identity configured would fail at that rather than at the claim.
export GIT_AUTHOR_NAME="cut-release-test" GIT_AUTHOR_EMAIL="test@example.invalid"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
# The script reads VERSION from the environment when no argument is given, so
# a caller's own VERSION would silently supply the tag these cases withhold.
unset VERSION
# And it reads GH for the forge's command line, which every case here supplies
# a stub for, so a caller's own would reach the real forge.
unset GH

step()   { printf '\n=== %s\n' "$*"; }
pass()   { printf '  ok: %s\n' "$*"; }
fail()   { printf '  FAIL: %s\n' "$*"; failures=$((failures + 1)); }

contains() {
  case "$1" in (*"$2"*) pass "$3" ;; (*) fail "$3 -- got: $1" ;; esac
}
missing() {
  case "$1" in (*"$2"*) fail "$3 -- got: $1" ;; (*) pass "$3" ;; esac
}

# readiness_section prints the delimited Markdown section the stub harness
# answers the release-readiness gate with, green or red, in the shape
# internal/cli/conformance.go renders: the verdict line the cut compares a
# stamped result against, and the markers it stamps between. What the real
# checks find is internal/conformance's own suite to say, not this one's.
readiness_section() {
  printf '<!-- yoyodyne:release-readiness -->\n'
  printf '## Release readiness\n\n'
  if [ "$1" = "red" ]; then
    printf 'The `release-readiness` workflow ended in **mismatch**.\n\n'
    printf -- '- **goals** — diverges — 1 admitted item(s)\n'
    printf -- '  - goals: stub-1: names a goal no goals document states\n'
  else
    printf 'The `release-readiness` workflow ended in **ready**.\n\n'
    printf -- '- **artifacts** — conforms — 1 artifact(s) across 1 home(s)\n'
  fi
  printf '<!-- /yoyodyne:release-readiness -->\n'
}

# pull_requests_of names where a fabricated repository's stub forge records the
# pull requests it was asked to open: beside the repository, never in it.
pull_requests_of() { printf '%s/%s-pull-requests.log' "$scratch" "$(basename "$1")"; }

# fabricate builds one scratch repository: $1 names it, $2 is "green", "red", or
# "dirty-exports" for the walkthrough, $3 is "green", "check-red", "build-red",
# "no-checksums" or "harness-red" for make, $4 is "present" (the default, notes
# already carrying a current readiness result), "unstamped" (notes with no
# result in them), "absent", or "draft-red" for this release's notes, $5 is
# what the stub forge command line answers about the default branch --
# "open" (the default), "protected" (the older per-branch protection),
# "protected-unreadable" (the same, asked by an account that may not read the
# protection endpoint), "ruleset" (a ruleset requiring a pull request), or "down" (a forge it cannot
# reach) -- and $6 is "green" (the default) or "red" for the release-readiness
# gate. Most cases want notes already stamped, an open branch, and a green
# gate, because the one they are about is further down.
fabricate() {
  local name="$1" walk="$2" mk="$3" notes="${4:-present}" forge="${5:-open}" readiness="${6:-green}"
  local project="$scratch/$name"

  mkdir -p "$project/scripts" "$project/bin"
  cp "$repository/scripts/cut-release.sh" "$project/scripts/cut-release.sh"

  # A stub harness for the release-readiness gate. The cut builds it and asks it
  # one question, and what it needs back is the delimited Markdown section and
  # an exit status that is the gate's answer -- so that is the whole of what
  # this answers.
  {
    printf '#!/usr/bin/env bash\n'
    printf 'set -euo pipefail\n'
    printf 'if [ "${1:-}" != "conformance" ]; then echo "stub yoyo: unexpected command ${1:-}" >&2; exit 2; fi\n'
    printf 'cat <<%s\n' "'MD'"
    readiness_section "$readiness"
    printf 'MD\n'
    if [ "$readiness" = "red" ]; then
      printf 'exit 1\n'
    fi
  } > "$project/bin/yoyo"
  chmod +x "$project/bin/yoyo"

  # A stub forge command line. The cut asks it two things: whether the default
  # branch is protected, both ways a forge can protect one, and to open the
  # pull request that carries a readiness result to that branch. It answers
  # the first as fabricated and records the second beside the repository
  # rather than in it -- a log in the working tree would be the dirty tree the
  # cut refuses -- one line per request, so a case can assert on what was
  # opened and how often.
  {
    printf '#!/usr/bin/env bash\n'
    printf 'set -euo pipefail\n'
    printf 'log="%s"\n' "$(pull_requests_of "$project")"
    printf 'case "${1:-} ${2:-}" in\n'
    printf '  "api repos/{owner}/{repo}/branches/main")\n'
    case "$forge" in
      protected|protected-unreadable) printf '    echo true\n' ;;
      down) printf '    echo "error connecting to api.forge.invalid" >&2; exit 1\n' ;;
      *)    printf '    echo false\n' ;;
    esac
    printf '    ;;\n'
    # The per-branch protection endpoint needs administrator rights, and the
    # forge answers an account without them with the 404 it gives an
    # unprotected branch -- which is what "protected-unreadable" fabricates.
    printf '  "api repos/{owner}/{repo}/branches/main/protection")\n'
    case "$forge" in
      protected) printf '    echo "required pull request reviews"\n' ;;
      down)      printf '    echo "error connecting to api.forge.invalid" >&2; exit 1\n' ;;
      *)         printf '    echo "HTTP 404: Branch not protected" >&2; exit 1\n' ;;
    esac
    printf '    ;;\n'
    printf '  "api repos/{owner}/{repo}/rules/branches/main")\n'
    if [ "$forge" = "ruleset" ]; then
      printf '    echo 1\n'
    else
      printf '    echo 0\n'
    fi
    printf '    ;;\n'
    printf '  "pr create")\n'
    printf '    printf "%%s\\n" "$*" >> "$log"\n'
    printf '    echo "https://forge.invalid/pull/$(wc -l < "$log" | tr -d " ")"\n'
    printf '    ;;\n'
    printf '  *) echo "stub gh: unexpected $*" >&2; exit 2 ;;\n'
    printf 'esac\n'
  } > "$project/bin/gh"
  chmod +x "$project/bin/gh"

  # A stub notes writer, so the notes gate is exercised without a tracker. The
  # real scripts/release-notes.sh reads the tracker's export and renders with
  # python3; what cut-release.sh needs from it is that it writes
  # docs/releases/<tag>.md under its own repository and says whether it could,
  # and that is what this does. A draft carries no readiness result, the way a
  # real one does not: the cut stamps that in afterwards.
  if [ "$notes" = "draft-red" ]; then
    cat > "$project/scripts/release-notes.sh" <<'SH'
#!/usr/bin/env bash
echo "release-notes: .beads/issues.jsonl is not here" >&2
exit 1
SH
  else
    cat > "$project/scripts/release-notes.sh" <<'SH'
#!/usr/bin/env bash
set -euo pipefail
repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
mkdir -p "$repository/docs/releases"
printf '# %s\n\n## Key functionality\n\n- **The scratch release** (`stub-1`)\n' "$1" \
  > "$repository/docs/releases/$1.md"
echo "wrote docs/releases/$1.md from 1 closed work item(s)"
SH
  fi
  chmod +x "$project/scripts/release-notes.sh"

  # The notes a cut is gated on are committed with the commit the tag names, so
  # a repository that has them has them in its history rather than beside it.
  # "present" is the ordinary day: the readiness result reached the notes
  # through an earlier cut's pull request, so this cut finds it current.
  if [ "$notes" = "present" ] || [ "$notes" = "unstamped" ]; then
    mkdir -p "$project/docs/releases"
    printf '# v0.3.0\n\n## Key functionality\n\n- **The scratch release** (`stub-1`)\n' \
      > "$project/docs/releases/v0.3.0.md"
    if [ "$notes" = "present" ]; then
      { printf '\n'; readiness_section green; } >> "$project/docs/releases/v0.3.0.md"
    fi
  fi

  case "$walk" in
    green)
      cat > "$project/scripts/walk-adoption.sh" <<'SH'
#!/usr/bin/env bash
echo "  ok: the documented adoption path works as written"
SH
      ;;
    dirty-exports)
      # What the real walkthrough does to the tracker on its way through: it
      # exercises bd, which rewrites the passive export beside it. The path is
      # resolved from the script rather than the caller's directory, because
      # the cut is run from wherever the operator happens to be.
      cat > "$project/scripts/walk-adoption.sh" <<'SH'
#!/usr/bin/env bash
echo "  ok: the documented adoption path works as written"
project="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
printf 'the walkthrough touched the tracker\n' >> "$project/.beads/issues.jsonl"
SH
      ;;
    *)
      cat > "$project/scripts/walk-adoption.sh" <<'SH'
#!/usr/bin/env bash
echo "  FAIL: README says Go 1.24 or newer, go.mod declares 1.9"
exit 1
SH
      ;;
  esac
  chmod +x "$project/scripts/walk-adoption.sh" "$project/scripts/cut-release.sh"

  # Tabs matter here, so the recipe lines are written with printf rather than a
  # heredoc an editor could helpfully reformat.
  {
    # `build` is what the cut runs before its release-readiness gate, because it
    # asks the harness rather than the repository. The stub binary is already on
    # disk, so a green build has nothing to do beyond saying it worked.
    printf 'build:\n'
    if [ "$mk" = "harness-red" ]; then
      printf '\t@echo "stub build is red"; exit 1\n'
    else
      printf '\t@echo "stub build succeeded"\n'
    fi
    printf 'check:\n'
    if [ "$mk" = "check-red" ]; then
      printf '\t@echo "stub check is red"; exit 1\n'
    else
      printf '\t@echo "stub check passed"\n'
    fi
    printf 'dist-verify:\n'
    if [ "$mk" = "build-red" ]; then
      printf '\t@echo "stub dist-verify is red"; exit 1\n'
    elif [ "$mk" = "no-checksums" ]; then
      # A build that succeeded and put its checksums somewhere else: what a
      # rename of the real dist recipe's output would look like from here.
      printf '\t@echo "stub built $(VERSION), checksums elsewhere"\n'
    else
      printf '\t@mkdir -p dist\n'
      printf '\t@echo "abc123  yoyo_$(VERSION)_stub.tar.gz" > dist/checksums.txt\n'
      printf '\t@echo "stub built $(VERSION)"\n'
    fi
  } > "$project/Makefile"

  printf '/dist/\n' > "$project/.gitignore"

  # The tracker's derived exports, tracked and clean, the way they are in a
  # repository that has adopted yoyo. Churn in them is what the cut leaves
  # where it lies.
  mkdir -p "$project/.beads"
  printf 'exported\n' > "$project/.beads/issues.jsonl"
  printf 'exported\n' > "$project/.beads/interactions.jsonl"

  git init -q "$project"
  # git init's default branch name varies by version; name it the way the
  # script expects to find a release being cut from.
  git -C "$project" symbolic-ref HEAD refs/heads/main
  git -C "$project" add -A
  git -C "$project" commit -qm "the commit a release would name"

  printf '%s' "$project"
}

# origin_for gives a fabricated repository a bare origin on disk, holding its
# main, so the remote gate is executed rather than only its unreachable path.
# With "protected" the origin refuses every push to main, the way a forge's
# branch protection does, unless the file the hook looks for is there -- which
# is how a case stands in for the forge merging a pull request.
origin_for() {
  local project="$1" protection="${2:-open}"
  local origin="$scratch/$(basename "$project")-origin.git"
  git init -q --bare "$origin"
  # The same reason fabricate names the project's branch, for the same varying
  # default -- and here it is the bare repository's HEAD, which is what a clone
  # of this origin checks out. Left at a default of "master" the origin holds a
  # main that its own HEAD does not name, and a clone of it gets no main at all:
  # the case that lets main move on pushes from such a clone, and on a machine
  # whose git defaults to "master" that push failed with "src refspec main does
  # not match any" rather than the case failing on what it was about.
  git -C "$origin" symbolic-ref HEAD refs/heads/main
  git -C "$project" remote add origin "$origin"
  git -C "$project" push -q origin main
  if [ "$protection" = "protected" ]; then
    # Pinned absolutely, so a machine whose global config points core.hooksPath
    # somewhere else does not quietly turn this fixture into no fixture.
    git -C "$origin" config core.hooksPath "$origin/hooks"
    cat > "$origin/hooks/pre-receive" <<SH
#!/usr/bin/env bash
while read -r old new ref; do
  if [ "\$ref" = "refs/heads/main" ] && [ ! -f "$origin/forge-merging" ]; then
    printf 'error: GH006: Protected branch update failed for refs/heads/main.\\nerror: Changes must be made through a pull request.\\n' >&2
    exit 1
  fi
done
SH
    chmod +x "$origin/hooks/pre-receive"
  fi
  printf '%s' "$origin"
}

# forge_merges stands in for the forge merging the pull request a cut opened:
# main gains a merge commit that is not the branch's commit, the way a real
# merge produces a commit of its own, and the checkout is brought to where
# origin/main then is, which is where the next cut has to start from.
forge_merges() {
  local project="$1" origin="$2" branch="$3"
  git -C "$project" fetch -q origin "refs/heads/$branch:refs/remotes/origin/$branch"
  git -C "$project" merge -q --no-ff -m "Merge pull request #1 from $branch" "refs/remotes/origin/$branch"
  touch "$origin/forge-merging"
  git -C "$project" push -q origin main
  rm -f "$origin/forge-merging"
}

# cut runs the fabricated copy of the verb and returns its output whatever it
# exits with; each case asserts on what it said and on what it left behind. The
# fabricated bin is put first on PATH so the forge command line the cut finds
# is the stub, on a machine that has the real one and on one that does not.
cut() {
  local project="$1"; shift
  PATH="$project/bin:$PATH" "$project/scripts/cut-release.sh" "$@" 2>&1 || true
}

tags() { git -C "$1" tag --list; }
branches() { git -C "$1" branch --list --format='%(refname:short)' 'release/*'; }
stamp_branch() { printf 'release/v0.3.0-readiness-%s' "$(git -C "$1" rev-parse --short "$2")"; }

step "a tag is required, and the checkout's describe default is not one"
project="$(fabricate no-tag green green)"
output="$(cut "$project")"
contains "$output" "no version given" "refuses with no tag at all"
contains "$output" "make release VERSION=" "the refusal names how to pass one"
missing "$output" "adoption walkthrough" "refuses before spending the walkthrough"

output="$(cut "$project" "v0.2.0-143-gf5e427a")"
contains "$output" "is not a release tag" "refuses \`git describe\` output, which VERSION defaults to"
output="$(cut "$project" "0.3.0")"
contains "$output" "is not a release tag" "refuses a tag with no leading v"
output="$(cut "$project" "v0.3")"
contains "$output" "is not a release tag" "refuses a tag that is not MAJOR.MINOR.PATCH"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written by any refusal"
else
  fail "a refused cut left tags behind: $(tags "$project")"
fi

step "a release is cut once"
project="$(fabricate existing-tag green green)"
git -C "$project" tag -a v0.3.0 -m v0.3.0
output="$(cut "$project" "v0.3.0")"
contains "$output" "v0.3.0 already exists" "refuses a tag that already exists"
contains "$output" "latest tag: v0.3.0" "the refusal names where the tags are up to"

step "the archives have to be the commit the tag names"
project="$(fabricate dirty-tree green green)"
printf 'uncommitted\n' > "$project/stray.txt"
# The exports are dirty too, so this is also the case that shows what the cut
# excuses does not extend to the file beside them.
printf 'churn\n' >> "$project/.beads/issues.jsonl"
output="$(cut "$project" "v0.3.0")"
contains "$output" "uncommitted changes" "refuses a dirty working tree"
contains "$output" "stray.txt" "the refusal names the file"
missing "$output" "issues.jsonl" "the refusal names what stands in the way, not the derived exports"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a tree dirty only in the tracker's derived exports is cut, and they are left where they lie"
# These are derived from a store that is authoritative elsewhere and nothing a
# release ships is built from them, so under a daily cadence refusing on them
# would stall most days. They are excluded from the tree check and never
# committed: a commit carrying them is one the default branch cannot take, and
# the tag names the commit origin already holds.
project="$(fabricate dirty-exports-only green green)"
printf 'churn\n' >> "$project/.beads/issues.jsonl"
printf 'churn\n' >> "$project/.beads/interactions.jsonl"
started_at="$(git -C "$project" rev-parse HEAD)"
output="$(cut "$project" "v0.3.0")"
missing "$output" "uncommitted changes" "does not refuse a tree dirty only in the exports"
contains "$output" "derived exports have changed" "it says up front that it found them changed"
contains "$output" "left where they lie" "and that it is leaving them"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "the cut proceeded and the tag was written"
else
  fail "expected v0.3.0 to be the only tag, got: $(tags "$project")"
fi
if [ "$(git -C "$project" rev-parse HEAD)" = "$started_at" ]; then
  pass "the cut made no commit"
else
  fail "the cut moved HEAD from $started_at to $(git -C "$project" rev-parse HEAD)"
fi
if [ "$(git -C "$project" rev-parse 'v0.3.0^{commit}')" = "$started_at" ]; then
  pass "the tag names the commit the cut started from"
else
  fail "the tag names $(git -C "$project" rev-parse 'v0.3.0^{commit}') rather than $started_at"
fi
if [ "$(git -C "$project" status --porcelain | sort | tr -d ' ' | tr '\n' ' ')" = "M.beads/interactions.jsonl M.beads/issues.jsonl " ]; then
  pass "the exports are still dirty, exactly as the cut found them"
else
  fail "the tree is not dirty in the exports alone: $(git -C "$project" status --porcelain)"
fi
# The refusal used to send the operator to `git stash`, and the stash-pop after
# a successful cut then conflicted with the exports the cut itself rewrote.
# Nothing is stashed now, so there is no pop to conflict.
if [ -z "$(git -C "$project" stash list)" ]; then
  pass "nothing was stashed, so there is no stash to pop"
else
  fail "the cut stashed something: $(git -C "$project" stash list)"
fi
contains "$output" "git push origin v0.3.0" "publishing is the tag alone"
missing "$output" "--atomic" "no branch goes with it, because origin already holds the commit"

step "the exports the gate dirties on its own way through are left where they lie too"
# The walkthrough exercises the tracker, so the tree it was asked to find clean
# is dirty again by the time the tag is placed. The tag is placed on the commit
# by hash, so what the walkthrough did to the working tree changes nothing
# about what it names.
project="$(fabricate walk-dirties-exports dirty-exports green)"
started_at="$(git -C "$project" rev-parse HEAD)"
output="$(cut "$project" "v0.3.0")"
missing "$output" "derived exports have changed" "the tree it was asked to find clean was clean"
if [ "$(tags "$project")" = "v0.3.0" ] && [ "$(git -C "$project" rev-parse 'v0.3.0^{commit}')" = "$started_at" ]; then
  pass "the cut proceeded and the tag names the commit the gates ran on"
else
  fail "expected v0.3.0 on $started_at, got: $(tags "$project") on $(git -C "$project" rev-parse 'v0.3.0^{commit}' 2>/dev/null)"
fi
if [ "$(git -C "$project" status --porcelain | tr -d ' ')" = "M.beads/issues.jsonl" ]; then
  pass "the export the walkthrough wrote is dirty and uncommitted"
else
  fail "the tree is not dirty in the walkthrough's export alone: $(git -C "$project" status --porcelain)"
fi

step "a release comes off the branch integration lands on"
project="$(fabricate wrong-branch green green)"
git -C "$project" checkout -q -b some-feature
output="$(cut "$project" "v0.3.0")"
contains "$output" "a release is cut from main" "refuses a cut from a feature branch"
contains "$output" "some-feature" "the refusal names the branch it is on"

step "a tag does not name a commit origin does not have"
project="$(fabricate diverged-from-origin green green)"
# A bare repository on disk is an origin a fetch can reach with no network, so
# the comparison itself is executed rather than only its unreachable path.
origin_for "$project" >/dev/null
shared="$(git -C "$project" rev-parse HEAD)"
git -C "$project" commit -q --allow-empty -m "a commit origin does not have"
output="$(cut "$project" "v0.3.0")"
contains "$output" "HEAD is not where origin/main is" "refuses a HEAD that has diverged from origin"
contains "$output" "$shared" "the refusal names the commit origin has"
missing "$output" "adoption walkthrough" "refuses before spending the walkthrough"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "and cuts when origin agrees, without pushing anything to it"
project="$(fabricate agrees-with-origin green green)"
origin="$(origin_for "$project")"
output="$(cut "$project" "v0.3.0")"
contains "$output" "origin/main agrees" "a reachable origin that agrees is checked and said so"
contains "$output" "origin/main is not protected, and the cut writes nothing to it all the same" "an open branch is detected and the cut says its path is the same"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "the tag was written"
else
  fail "expected v0.3.0 to be the only tag, got: $(tags "$project")"
fi
# The strongest form of "it does not publish": there was a remote to push to,
# and the cut left nothing on it.
if [ -z "$(git -C "$origin" tag --list)" ]; then
  pass "the tag was not pushed, because publishing is the operator's own command"
else
  fail "the cut pushed a tag to origin: $(git -C "$origin" tag --list)"
fi
if [ "$(git -C "$origin" for-each-ref --format='%(refname)' | tr '\n' ' ')" = "refs/heads/main " ]; then
  pass "and no branch either, because the notes already carried a current result"
else
  fail "the cut left refs on origin: $(git -C "$origin" for-each-ref --format='%(refname)' | tr '\n' ' ')"
fi

step "the tag names the commit the gates ran on even when main has moved on behind them"
# Integration lands continuously, so between the first fetch and the tag origin
# may hold more. The gates ran on HEAD, so HEAD is what the tag names, and the
# cut says main has moved rather than racing after it.
project="$(fabricate main-moves-on green green)"
origin="$(origin_for "$project")"
# The walkthrough is where the time goes, so that is where the stub lets main
# move: it pushes a commit to origin from a second clone on its way through.
cat > "$project/scripts/walk-adoption.sh" <<SH
#!/usr/bin/env bash
echo "  ok: the documented adoption path works as written"
git -C "$scratch/main-moves-on-elsewhere" commit -q --allow-empty -m "landed while the gates ran"
git -C "$scratch/main-moves-on-elsewhere" push -q origin main
SH
git -C "$project" commit -qam "a walkthrough that lets main move"
git -C "$project" push -q origin main
git clone -q "$origin" "$scratch/main-moves-on-elsewhere"
started_at="$(git -C "$project" rev-parse HEAD)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "origin/main has moved on to" "the cut says main moved while the gates ran"
contains "$output" "which main still holds" "and that the commit it tags is still on it"
if [ "$(git -C "$project" rev-parse 'v0.3.0^{commit}' 2>/dev/null)" = "$started_at" ]; then
  pass "the tag names the commit the gates ran on"
else
  fail "the tag names $(git -C "$project" rev-parse 'v0.3.0^{commit}' 2>/dev/null) rather than $started_at"
fi
# And a main rewritten underneath the cut is refused, because the tag would
# name a commit the product no longer has.
project="$(fabricate main-rewritten green green)"
origin="$(origin_for "$project")"
cat > "$project/scripts/walk-adoption.sh" <<SH
#!/usr/bin/env bash
echo "  ok: the documented adoption path works as written"
git -C "$scratch/main-rewritten-elsewhere" commit -q --amend --allow-empty -m "main rewritten while the gates ran"
git -C "$scratch/main-rewritten-elsewhere" push -q --force origin main
SH
git -C "$project" commit -qam "a walkthrough that rewrites main"
git -C "$project" push -q origin main
git clone -q "$origin" "$scratch/main-rewritten-elsewhere"
output="$(cut "$project" "v0.3.0")"
contains "$output" "has been rewritten since the gates started" "refuses a main rewritten underneath the cut"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a release with no notes drafts them and refuses, and goes on once they are committed"
project="$(fabricate no-notes green green absent)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "has no notes yet" "says the notes are missing"
contains "$output" "drafted docs/releases/v0.3.0.md" "names the file it drafted"
contains "$output" "commit it, then cut v0.3.0 again" "names what to do with it"
missing "$output" "documented adoption path works" "refuses before spending the walkthrough"
missing "$output" "stub built" "refuses before building anything"
if [ -f "$project/docs/releases/v0.3.0.md" ]; then
  pass "the draft is really on disk, which is the one thing the refusal left behind"
else
  fail "the refusal claimed a draft that is not there"
fi
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi
# The second half of the same story: the operator reads the draft, places each
# item, commits it, and the cut goes on. These are the commands
# docs/developing-yoyo.md tells them to run, spelled the same way on purpose --
# the draft is a file git has never seen, so `git commit -a` would stage
# nothing and stop with "no changes added to commit". A test that staged it
# some easier way would leave that hole in the documentation.
git -C "$project" add docs/releases/v0.3.0.md
git -C "$project" commit -qm "v0.3.0 release notes"
output="$(cut "$project" "v0.3.0")"
contains "$output" "docs/releases/v0.3.0.md is present" "the gate passes once the notes are committed"
contains "$output" "the notes carry no readiness result" "and the next gate finds the draft carries no readiness result"

step "a readiness result the notes do not carry is committed on a branch, and the cut stops there"
# With no origin to push to, the branch stays local and the refusal says so;
# what it holds is the commit the pull request would carry. The checkout is
# untouched: the notes on disk are the committed ones, nothing is staged, and
# no branch is checked out.
project="$(fabricate stamp-locally green green unstamped)"
started_at="$(git -C "$project" rev-parse HEAD)"
before="$(cat "$project/docs/releases/v0.3.0.md")"
output="$(cut "$project" "v0.3.0")"
branch="$(stamp_branch "$project" "$started_at")"
contains "$output" "stale: the notes carry no readiness result" "says why the notes are not current"
contains "$output" "committed docs/releases/v0.3.0.md on $branch" "names the branch it committed on"
contains "$output" "origin is unreachable, so it was committed on the local branch" "and says the branch could not be pushed"
contains "$output" "that branch is the only thing this left behind" "the refusal says what it left"
missing "$output" "documented adoption path works" "stops before spending the walkthrough"
missing "$output" "stub built" "stops before building anything"
if [ "$(branches "$project")" = "$branch" ]; then
  pass "the branch is really there"
else
  fail "expected $branch, got: $(branches "$project")"
fi
if [ "$(git -C "$project" branch --show-current)" = "main" ] && [ "$(git -C "$project" rev-parse HEAD)" = "$started_at" ]; then
  pass "the checkout is still on main, at the commit it started from"
else
  fail "the checkout moved: $(git -C "$project" branch --show-current) at $(git -C "$project" rev-parse HEAD)"
fi
if [ "$(cat "$project/docs/releases/v0.3.0.md")" = "$before" ] && [ -z "$(git -C "$project" status --porcelain)" ]; then
  pass "the working tree is exactly as it was"
else
  fail "the cut touched the working tree: $(git -C "$project" status --porcelain)"
fi
if [ "$(git -C "$project" rev-parse "$branch^")" = "$started_at" ]; then
  pass "the branch's commit sits on the commit origin would hold"
else
  fail "the branch's commit sits on $(git -C "$project" rev-parse "$branch^")"
fi
committed="$(git -C "$project" diff-tree --no-commit-id --name-only -r "$branch" | tr '\n' ' ')"
if [ "$committed" = "docs/releases/v0.3.0.md " ]; then
  pass "the commit holds the notes and nothing else"
else
  fail "the commit holds: $committed"
fi
contains "$(git -C "$project" show "$branch:docs/releases/v0.3.0.md")" "ended in **ready**" "the notes on the branch carry the result"
contains "$(git -C "$project" show "$branch:docs/releases/v0.3.0.md")" "The scratch release" "and everything the product manager wrote around it is untouched"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the stopped cut left tags behind: $(tags "$project")"
fi
# Running it again at the same commit finds the branch rather than a second one.
output="$(cut "$project" "v0.3.0")"
contains "$output" "already on the branch $branch" "a second cut at the same commit finds the branch it made"
contains "$output" "nothing was written" "and writes nothing more"

step "a stale result is replaced rather than accumulated, and a current one is left alone"
# The notes are written once and then edited, and a cut writes into a file the
# product manager owns. Replacing between the markers rather than appending is
# what keeps a second stamp from leaving two sections that disagree. What makes
# a result stale is its verdict or its pin, not its counts: those move with the
# tracker every day, and a stamp held to them would never be current.
project="$(fabricate stamp-stale green green unstamped)"
{
  printf '\n<!-- yoyodyne:release-readiness -->\n## Release readiness\n\n'
  printf 'The `release-readiness` workflow ended in **mismatch**.\n\n'
  printf 'A result from an earlier reading.\n'
  printf '<!-- /yoyodyne:release-readiness -->\n'
} >> "$project/docs/releases/v0.3.0.md"
git -C "$project" add docs/releases/v0.3.0.md
git -C "$project" commit -qm "an earlier readiness result"
started_at="$(git -C "$project" rev-parse HEAD)"
output="$(cut "$project" "v0.3.0")"
branch="$(stamp_branch "$project" "$started_at")"
contains "$output" "ended in **mismatch** and this reading ended in **ready**" "says what made the recorded result stale"
notes="$(git -C "$project" show "$branch:docs/releases/v0.3.0.md")"
sections="$(printf '%s\n' "$notes" | grep -c '^<!-- yoyodyne:release-readiness -->$')"
if [ "$sections" = "1" ]; then
  pass "the notes on the branch carry one readiness section"
else
  fail "the notes on the branch carry $sections readiness sections"
fi
missing "$notes" "A result from an earlier reading" "the earlier result was replaced rather than left below the new one"
contains "$notes" "ended in **ready**" "and the section that is there is this cut's"
# A result that differs only in its counts is current: the day's reading is
# the same verdict from the same definition.
project="$(fabricate stamp-current green green unstamped)"
{
  printf '\n<!-- yoyodyne:release-readiness -->\n## Release readiness\n\n'
  printf 'The `release-readiness` workflow ended in **ready**.\n\n'
  printf -- '- **artifacts** — conforms — 26 artifact(s) across 3 home(s)\n'
  printf '<!-- /yoyodyne:release-readiness -->\n'
} >> "$project/docs/releases/v0.3.0.md"
git -C "$project" add docs/releases/v0.3.0.md
git -C "$project" commit -qm "yesterday's readiness result"
output="$(cut "$project" "v0.3.0")"
contains "$output" "current: the notes record that the workflow ended in **ready**" "a result with the same verdict is current whatever its counts"
if [ -z "$(branches "$project")" ] && [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "no branch was made, and the cut went through to the tag"
else
  fail "branches: $(branches "$project"); tags: $(tags "$project")"
fi
# A result pinned to another definition is stale even with the same verdict,
# because it describes a check set this cut did not run.
project="$(fabricate stamp-repinned green green unstamped)"
{
  printf '\n<!-- yoyodyne:release-readiness -->\n## Release readiness\n\n'
  printf 'The `release-readiness` workflow ended in **ready**.\n\n'
  printf 'Pinned to `wf-0000`.\n'
  printf '<!-- /yoyodyne:release-readiness -->\n'
} >> "$project/docs/releases/v0.3.0.md"
git -C "$project" add docs/releases/v0.3.0.md
git -C "$project" commit -qm "a readiness result from another definition"
output="$(cut "$project" "v0.3.0")"
contains "$output" "pinned to \`wf-0000\`" "a result pinned to another definition is stale"
if [ -n "$(branches "$project")" ] && [ -z "$(tags "$project")" ]; then
  pass "it was restamped on a branch and the cut stopped"
else
  fail "branches: $(branches "$project"); tags: $(tags "$project")"
fi

step "the notes commit has to reach origin before the cut goes on, and then the result follows it through a pull request"
# The documented loop, executed against an origin the cut can reach: the draft,
# the notes commit that is not enough on its own, the push, the cut that
# commits the readiness result on a branch and opens the pull request, the
# merge, and the cut that tags.
project="$(fabricate notes-not-pushed green green absent)"
origin="$(origin_for "$project")"
output="$(cut "$project" "v0.3.0")"
contains "$output" "drafted docs/releases/v0.3.0.md" "the first cut drafts the notes"
git -C "$project" add docs/releases/v0.3.0.md
git -C "$project" commit -qm "v0.3.0 release notes"
output="$(cut "$project" "v0.3.0")"
contains "$output" "HEAD is not where origin/main is" "committing the notes is not enough on its own"
missing "$output" "is present and committed" "it refuses before the notes gate, so nothing further is spent"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi
git -C "$project" push -q origin main
started_at="$(git -C "$project" rev-parse HEAD)"
output="$(cut "$project" "v0.3.0")"
branch="$(stamp_branch "$project" "$started_at")"
contains "$output" "origin/main agrees" "pushing them satisfies the remote gate"
contains "$output" "docs/releases/v0.3.0.md is present" "and the notes gate passes behind it"
contains "$output" "pushed $branch to origin" "the readiness result is committed on a branch and pushed"
contains "$output" "opened https://forge.invalid/pull/1" "and a pull request is opened for it"
contains "$output" "a pull request opened for it, and the cut stopped there" "and the cut says so and stops"
if [ -n "$(git -C "$origin" rev-parse -q --verify "refs/heads/$branch")" ]; then
  pass "origin really has the branch"
else
  fail "origin does not have $branch"
fi
contains "$(cat "$(pull_requests_of "$project")")" "--base main --head $branch" "the pull request is from that branch against main"
forge_merges "$project" "$origin" "$branch"
output="$(cut "$project" "v0.3.0")"
contains "$output" "current: the notes record that the workflow ended in **ready**" "after the merge the result is on main and current"
if [ "$(tags "$project")" = "v0.3.0" ] && [ "$(git -C "$project" rev-parse 'v0.3.0^{commit}')" = "$(git -C "$origin" rev-parse main)" ]; then
  pass "the tag was written, and it names the commit origin's main holds"
else
  fail "tags: $(tags "$project"); the tag names $(git -C "$project" rev-parse 'v0.3.0^{commit}' 2>/dev/null) and origin/main is $(git -C "$origin" rev-parse main)"
fi

step "against a protected default branch: the two-cut loop, through to the pushed tag"
# The case the verb was rebuilt for. main refuses every push that is not the
# forge merging a pull request, the exports are dirty throughout, and the loop
# has to close on a pushed tag with no commit of the cut's ever reaching main.
project="$(fabricate protected-loop green green unstamped protected)"
origin="$(origin_for "$project" protected)"
printf 'churn\n' >> "$project/.beads/issues.jsonl"
printf 'churn\n' >> "$project/.beads/interactions.jsonl"
started_at="$(git -C "$project" rev-parse HEAD)"
branch="$(stamp_branch "$project" "$started_at")"
# The fixture is real before anything rests on it: a direct push to main is
# what the hook refuses, so the cases below are about the cut rather than
# about a hook that never fired.
direct="$(git -C "$project" commit-tree "$started_at^{tree}" -p "$started_at" -m "a commit main will not take directly")"
if refused="$(git -C "$project" push origin "$direct:refs/heads/main" 2>&1)"; then
  fail "the fixture's origin took a direct push to main, so the cases below would prove nothing"
else
  contains "$refused" "Changes must be made through a pull request" "the fixture's origin refuses a direct push to main the way the forge does"
fi
output="$(cut "$project" "v0.3.0")"
contains "$output" "origin/main is protected (by branch protection, with required pull request reviews): a change reaches it only through a pull request" "the protection is detected, and which rules apply is said"
missing "$output" "stub build succeeded" "before anything is built"
contains "$output" "reaches it only through" "and the cut says which step changes because of it"
contains "$output" "publishing is the tag alone" "and that the push is the tag alone"
contains "$output" "committed docs/releases/v0.3.0.md on $branch" "the result is committed on a branch"
contains "$output" "pushed $branch to origin" "which the protected origin accepts"
contains "$output" "opened https://forge.invalid/pull/1" "and its pull request is opened"
contains "$output" "Merge it, then cut v0.3.0 again" "and the cut says what closes the loop"
missing "$output" "documented adoption path works" "stops before spending the walkthrough"
missing "$output" "stub built" "stops before building anything"
if [ "$(git -C "$origin" rev-parse main)" = "$started_at" ]; then
  pass "main on origin is exactly where it was"
else
  fail "the cut moved origin's main to $(git -C "$origin" rev-parse main)"
fi
if [ "$(git -C "$project" rev-parse HEAD)" = "$started_at" ] && [ "$(git -C "$project" branch --show-current)" = "main" ]; then
  pass "the checkout is on main at the commit it started from"
else
  fail "the checkout moved: $(git -C "$project" branch --show-current) at $(git -C "$project" rev-parse HEAD)"
fi
committed="$(git -C "$project" diff-tree --no-commit-id --name-only -r "$branch" | tr '\n' ' ')"
if [ "$committed" = "docs/releases/v0.3.0.md " ]; then
  pass "the exports appear in no commit of the cut's"
else
  fail "the branch's commit holds: $committed"
fi
# The operator runs it again before the merge: the branch is found, not
# pushed over, and no second pull request is opened.
output="$(cut "$project" "v0.3.0")"
contains "$output" "already on the branch $branch" "a second cut before the merge finds the branch on origin"
if [ "$(wc -l < "$(pull_requests_of "$project")" | tr -d ' ')" = "1" ]; then
  pass "and opens no second pull request"
else
  fail "pull requests opened: $(cat "$(pull_requests_of "$project")")"
fi
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written by either cut"
else
  fail "a stopped cut left tags behind: $(tags "$project")"
fi
# And from a checkout that never made the branch -- a second machine, or the
# harness cutting from a fresh clone -- origin is what says it is there.
git -C "$project" branch -q -D "$branch"
output="$(cut "$project" "v0.3.0")"
contains "$output" "already on the branch $branch" "a cut from a checkout without the branch finds it on origin"
if [ "$(wc -l < "$(pull_requests_of "$project")" | tr -d ' ')" = "1" ]; then
  pass "and opens no second pull request either"
else
  fail "pull requests opened: $(cat "$(pull_requests_of "$project")")"
fi
forge_merges "$project" "$origin" "$branch"
merged="$(git -C "$origin" rev-parse main)"
# The local branch was deleted just above, and forge_merges fetches origin's
# into a remote-tracking ref, so that is where the branch's own commit is to be
# read from. Naming the branch bare here resolved to nothing: git printed a
# fatal and the comparison was against an empty string, which is a claim that
# passes whatever the merge did.
if [ "$merged" != "$(git -C "$project" rev-parse "origin/$branch")" ]; then
  pass "the merge is a commit of its own, as a pull request's merge is"
else
  fail "the merge is the branch's own commit"
fi
output="$(cut "$project" "v0.3.0")"
contains "$output" "current: the notes record that the workflow ended in **ready**" "the second cut finds the result current on main"
contains "$output" "documented adoption path works" "and spends the walkthrough"
contains "$output" "stub built v0.3.0" "and builds the archives"
contains "$output" "git push origin v0.3.0" "publishing is the tag alone"
missing "$output" "--atomic" "no branch goes with it"
if [ "$(tags "$project")" = "v0.3.0" ] && [ "$(git -C "$project" rev-parse 'v0.3.0^{commit}')" = "$merged" ]; then
  pass "the tag names the commit origin's main holds"
else
  fail "tags: $(tags "$project"); the tag names $(git -C "$project" rev-parse 'v0.3.0^{commit}' 2>/dev/null) and origin/main is $merged"
fi
if [ "$(git -C "$project" rev-parse HEAD)" = "$merged" ]; then
  pass "the second cut made no commit either"
else
  fail "the second cut moved HEAD to $(git -C "$project" rev-parse HEAD)"
fi
if [ "$(git -C "$project" status --porcelain | sort | tr -d ' ' | tr '\n' ' ')" = "M.beads/interactions.jsonl M.beads/issues.jsonl " ]; then
  pass "the exports are still dirty and uncommitted, throughout"
else
  fail "the tree is not dirty in the exports alone: $(git -C "$project" status --porcelain)"
fi
# The push the cut printed, executed against the protected origin: the tag
# alone, which protection has no say over.
if pushed="$(git -C "$project" push origin v0.3.0 2>&1)"; then
  pass "the push the cut printed is accepted by the protected origin"
else
  fail "the push the cut printed was refused: $pushed"
fi
if [ "$(git -C "$origin" rev-parse 'v0.3.0^{commit}' 2>/dev/null)" = "$merged" ]; then
  pass "origin has the tag, on the commit its main holds"
else
  fail "origin's tags: $(git -C "$origin" tag --list)"
fi

step "a ruleset requiring a pull request is protection too"
project="$(fabricate ruleset-protected green green present ruleset)"
origin_for "$project" >/dev/null
output="$(cut "$project" "v0.3.0")"
contains "$output" "origin/main is protected (by a ruleset)" "a ruleset the older protection query does not see is found by the rules query"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "and a cut whose notes already carry the result goes through"
else
  fail "expected v0.3.0, got: $(tags "$project")"
fi

step "a branch protected to an account that may not read its protection still reads as protected"
# The protection endpoint answers 404 to an account without administrator
# rights, which is what it answers for an unprotected branch; the branch's own
# 'protected' field is what decides, and the endpoint only says which rules.
project="$(fabricate protected-unreadable green green present protected-unreadable)"
origin_for "$project" >/dev/null
output="$(cut "$project" "v0.3.0")"
contains "$output" "origin/main is protected (by branch protection, whose rules this account cannot read)" "a 404 from the protection endpoint does not read a protected branch as open"
if printf '%s' "$output" | grep -qF "is not protected"; then
  fail "the cut called a protected branch open"
else
  pass "and the cut never calls it open"
fi
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "and a cut whose notes already carry the result goes through"
else
  fail "expected v0.3.0, got: $(tags "$project")"
fi

step "a forge that cannot be asked, and a forge command line that is not installed, are named rather than guessed"
project="$(fabricate forge-down green green present down)"
origin_for "$project" >/dev/null
output="$(cut "$project" "v0.3.0")"
contains "$output" "SKIPPED: whether origin/main is protected was not checked (gh could not ask the forge" "a forge that does not answer is named as unchecked"
contains "$output" "The cut writes" "and the cut says its path is the same"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "and the cut goes through"
else
  fail "expected v0.3.0, got: $(tags "$project")"
fi
project="$(fabricate no-gh green green unstamped)"
origin="$(origin_for "$project")"
started_at="$(git -C "$project" rev-parse HEAD)"
branch="$(stamp_branch "$project" "$started_at")"
output="$(GH="$scratch/no-such-gh" cut "$project" "v0.3.0")"
contains "$output" "is not installed). The cut writes" "without gh the protection question is named as unchecked"
contains "$output" "pushed $branch to origin" "the branch is still pushed"
contains "$output" "is not installed, so open the pull request for $branch against main yourself" "and the operator is told to open the pull request"
if [ -n "$(git -C "$origin" rev-parse -q --verify "refs/heads/$branch")" ] && [ -z "$(tags "$project")" ]; then
  pass "origin has the branch and no tag was written"
else
  fail "origin's refs: $(git -C "$origin" for-each-ref --format='%(refname)' | tr '\n' ' ')"
fi
if [ ! -f "$(pull_requests_of "$project")" ]; then
  pass "no pull request was opened, because nothing was there to open one"
else
  fail "a pull request was opened without gh: $(cat "$(pull_requests_of "$project")")"
fi

step "a cut whose notes cannot be drafted refuses rather than cutting without them"
project="$(fabricate undraftable-notes green green draft-red)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "notes could not be drafted" "refuses the cut"
contains "$output" ".beads/issues.jsonl is not here" "the drafting failure is shown rather than swallowed"
missing "$output" "documented adoption path works" "refuses before spending the walkthrough"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a system that no longer matches what it records refuses the tag"
# A tag says the system matches its recorded intent, so a divergence refuses it
# and names what diverged. The notes are left exactly as the operator committed
# them, and no branch is made, because nothing is written until every gate is
# green.
project="$(fabricate readiness-red green green present open red)"
before="$(cat "$project/docs/releases/v0.3.0.md")"
output="$(cut "$project" "v0.3.0")"
contains "$output" "release readiness is red" "refuses the cut"
contains "$output" "names a goal no goals document states" "the mismatch is named rather than swallowed"
contains "$output" "nothing was written" "the refusal says nothing was left behind"
missing "$output" "documented adoption path works" "refuses before spending the walkthrough"
missing "$output" "stub built" "refuses before building anything"
if [ "$(cat "$project/docs/releases/v0.3.0.md")" = "$before" ]; then
  pass "the notes are exactly as they were committed"
else
  fail "the refused cut wrote to the notes"
fi
if [ -z "$(branches "$project")" ]; then
  pass "no branch was made"
else
  fail "the refused cut left branches behind: $(branches "$project")"
fi
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a harness that will not build leaves readiness unchecked and the cut refused"
# The gate asks the harness, so a build that fails is a gate that never ran --
# which must refuse rather than pass for want of an answer.
project="$(fabricate readiness-unbuildable green harness-red)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "release readiness was never checked" "refuses rather than cutting on an unasked question"
missing "$output" "documented adoption path works" "refuses before spending the walkthrough"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a red walkthrough refuses the cut and names the failure"
project="$(fabricate red-walk red green)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "adoption walkthrough is red" "refuses the cut"
contains "$output" "go.mod declares 1.9" "the walkthrough's failing claim is named"
contains "$output" "nothing was written" "the refusal says nothing was left behind"
missing "$output" "stub check passed" "refuses before running the checks"
missing "$output" "stub built" "refuses before building anything"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a red check refuses the cut"
project="$(fabricate red-check green check-red)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "make check is red" "refuses the cut"
missing "$output" "stub built" "refuses before building anything"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written"
else
  fail "the refused cut left tags behind: $(tags "$project")"
fi

step "a failed build leaves no tag to undo"
project="$(fabricate red-build green build-red)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "release build for v0.3.0 failed" "refuses the cut"
if [ -z "$(tags "$project")" ]; then
  pass "no tag was written, because the tag is written last"
else
  fail "the failed build left tags behind: $(tags "$project")"
fi

step "green all the way through: one invocation, a tagged build with checksums"
project="$(fabricate green green green)"
started_at="$(git -C "$project" rev-parse HEAD)"
output="$(cut "$project" "v0.3.0")"
contains "$output" "SKIPPED: origin is unreachable, so whether HEAD is current was not checked" "an origin it cannot reach is named as unchecked rather than passed over"
contains "$output" "SKIPPED: whether origin/main is protected was not checked (origin is unreachable)" "and so is whether it is protected"
contains "$output" "docs/releases/v0.3.0.md is present" "the notes gate ran"
contains "$output" "Release readiness" "the readiness gate ran and its section was shown"
contains "$output" "current: the notes record that the workflow ended in **ready**" "the notes already carry it"
contains "$output" "documented adoption path works" "the walkthrough ran"
contains "$output" "stub check passed" "the checks ran"
contains "$output" "stub built v0.3.0" "the archives were built for the tag"
contains "$output" "yoyo_v0.3.0_stub.tar.gz" "the checksums are reported"
contains "$output" "git push origin v0.3.0" "publishing is the tag alone"
missing "$output" "--atomic" "no branch goes with it"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "the tag was written"
else
  fail "expected v0.3.0 to be the only tag, got: $(tags "$project")"
fi
if [ "$(git -C "$project" rev-parse v0.3.0^{commit})" = "$started_at" ] && [ "$(git -C "$project" rev-parse HEAD)" = "$started_at" ]; then
  pass "the tag names the commit the archives were built from, and the cut made no commit"
else
  fail "the tag names $(git -C "$project" rev-parse v0.3.0^{commit}); HEAD is $(git -C "$project" rev-parse HEAD); started at $started_at"
fi
if [ -n "$(git -C "$project" cat-file -t v0.3.0 | grep -x tag || true)" ]; then
  pass "the tag is annotated"
else
  fail "the tag is not annotated"
fi
if [ -z "$(branches "$project")" ]; then
  pass "no branch was made, because the notes already carried the result"
else
  fail "the cut left branches behind: $(branches "$project")"
fi
# Nothing left the repository: a cut that had pushed would have needed a
# remote, and this one has none.
if [ -z "$(git -C "$project" remote)" ]; then
  pass "the cut needed no remote, because it does not publish"
else
  fail "the scratch repository gained a remote"
fi

step "a finished cut is never reported as a failure"
# Once the tag exists the cut has happened, so nothing left to print may fail
# it: an operator told the release failed, holding a tag that is real and
# never shown the push it needs, is worse off than one told nothing.
project="$(fabricate silent-checksums green no-checksums)"
if output="$(PATH="$project/bin:$PATH" "$project/scripts/cut-release.sh" "v0.3.0" 2>&1)"; then status=0; else status=$?; fi
if [ "$status" = "0" ]; then
  pass "a cut whose checksums are not where it looked still exits 0"
else
  fail "the cut exited $status after tagging -- got: $output"
fi
contains "$output" "is not where it was expected" "it says the checksums were not found"
contains "$output" "git push origin v0.3.0" "the push the tag needs is still printed"
if [ "$(tags "$project")" = "v0.3.0" ]; then
  pass "the tag it reported is really there"
else
  fail "expected v0.3.0, got: $(tags "$project")"
fi

# cut_automatic runs the fabricated verb the way the harness runs it, as the
# gates of a cut the harness makes itself, and sets status to how it exited:
# the harness tells a refusal over preparation from a red gate by it.
cut_automatic() {
  local project="$1"; shift
  if output="$(cd "$project" && PATH="$project/bin:$PATH" YOYODYNE_RELEASE_CUT=automatic \
      "$project/scripts/cut-release.sh" "$@" 2>&1)"; then status=0; else status=$?; fi
}

# detach leaves a fabricated repository's checkout on a detached HEAD at the
# commit it is on, which is how the harness's own worktree for a cut stands.
detach() { git -C "$1" checkout -q --detach HEAD; }

step "the harness's cut: missing notes hand the release back, drafting nothing"
project="$(fabricate automatic-absent green green absent)"
origin_for "$project" >/dev/null
detach "$project"
cut_automatic "$project" "v0.3.0"
if [ "$status" = "3" ]; then pass "it exits 3, the status that hands preparation back"; else fail "it exited $status -- got: $output"; fi
contains "$output" "the harness does not draft them" "the refusal says the notes are the release item's run to write"
if [ ! -e "$project/docs/releases/v0.3.0.md" ]; then pass "nothing was drafted"; else fail "the harness's cut drafted notes"; fi
if [ -z "$(tags "$project")" ]; then pass "no tag was written"; else fail "the refused cut left tags behind: $(tags "$project")"; fi

step "the harness's cut: a readiness result not in the notes hands the release back, opening nothing"
project="$(fabricate automatic-unstamped green green unstamped)"
origin="$(origin_for "$project")"
detach "$project"
cut_automatic "$project" "v0.3.0"
if [ "$status" = "3" ]; then pass "it exits 3"; else fail "it exited $status -- got: $output"; fi
contains "$output" "opens no pull request" "the refusal says the stamp is the release item's run to land"
missing "$output" "adoption walkthrough" "it refuses before spending the walkthrough"
if [ -z "$(branches "$project")" ]; then pass "no branch was committed"; else fail "the harness's cut committed $(branches "$project")"; fi
if [ ! -s "$(pull_requests_of "$project")" ]; then pass "no pull request was opened"; else fail "the harness's cut opened a pull request"; fi
if [ "$(git -C "$origin" for-each-ref --format='%(refname)' | tr '\n' ' ')" = "refs/heads/main " ]; then
  pass "and nothing reached origin"
else
  fail "the harness's cut left refs on origin: $(git -C "$origin" for-each-ref --format='%(refname)' | tr '\n' ' ')"
fi

step "the harness's cut: origin has to be reachable"
project="$(fabricate automatic-unreachable green green)"
detach "$project"
cut_automatic "$project" "v0.3.0"
if [ "$status" = "1" ]; then pass "it exits 1"; else fail "it exited $status -- got: $output"; fi
contains "$output" "origin is unreachable" "it says why"
missing "$output" "SKIPPED" "it does not pass over the check the way a person's cut does"

step "the harness's cut: a detached HEAD origin does not hold is refused"
project="$(fabricate automatic-diverged green green)"
origin_for "$project" >/dev/null
git -C "$project" commit -q --allow-empty -m "a commit origin does not have"
detach "$project"
cut_automatic "$project" "v0.3.0"
if [ "$status" = "1" ]; then pass "it exits 1"; else fail "it exited $status -- got: $output"; fi
contains "$output" "HEAD is not where origin/main is" "it refuses as a person's cut does"

step "the harness's cut: a red gate is told apart from preparation"
project="$(fabricate automatic-check-red green check-red)"
origin_for "$project" >/dev/null
detach "$project"
cut_automatic "$project" "v0.3.0"
if [ "$status" = "1" ]; then pass "a red make check exits 1, not 3"; else fail "it exited $status -- got: $output"; fi
if [ -z "$(tags "$project")" ]; then pass "no tag was written"; else fail "the refused cut left tags behind: $(tags "$project")"; fi

step "the harness's cut: green all the way through names the commit and tags nothing"
project="$(fabricate automatic-green green green)"
origin="$(origin_for "$project")"
detach "$project"
checked="$(git -C "$project" rev-parse HEAD)"
cut_automatic "$project" "v0.3.0"
if [ "$status" = "0" ]; then pass "it exits 0"; else fail "it exited $status -- got: $output"; fi
contains "$output" "checked commit: $checked" "it names the commit the gates ran on"
contains "$output" "stub built v0.3.0" "the release build still ran as a gate"
missing "$output" "git push origin" "it prints no push, because the push is the harness's"
if [ -z "$(tags "$project")" ]; then pass "no tag was written; the tag is the harness's"; else fail "the harness's cut tagged: $(tags "$project")"; fi
if [ -z "$(git -C "$origin" tag --list)" ]; then pass "and nothing was pushed"; else fail "the harness's cut pushed $(git -C "$origin" tag --list)"; fi

step "the harness's cut is still refused off another branch"
project="$(fabricate automatic-branch green green)"
origin_for "$project" >/dev/null
git -C "$project" checkout -q -b feature
cut_automatic "$project" "v0.3.0"
if [ "$status" = "1" ]; then pass "it exits 1"; else fail "it exited $status -- got: $output"; fi
contains "$output" "this checkout is on \"feature\"" "it names the branch"

step "make release passes the tag through, and withholds the describe default"
wiring="$(make -C "$repository" -n release VERSION=v9.9.9 2>&1 || true)"
contains "$wiring" "scripts/cut-release.sh v9.9.9" "make release VERSION=<tag> reaches the verb with the tag"
wiring="$(make -C "$repository" -n release 2>&1 || true)"
missing "$wiring" "cut-release.sh v" "make release with no VERSION passes no tag, so the verb asks for one"
wiring="$(make -C "$repository" -n release-notes VERSION=v9.9.9 2>&1 || true)"
contains "$wiring" "scripts/release-notes.sh v9.9.9" "make release-notes VERSION=<tag> reaches the notes writer with the tag"
wiring="$(make -C "$repository" -n release-notes 2>&1 || true)"
missing "$wiring" "release-notes.sh v" "make release-notes with no VERSION passes no tag either"

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'cut-release.sh refuses what it should and cuts what it should\n'
else
  printf '%d claim(s) did not hold\n' "$failures"
fi
exit "$failures"
