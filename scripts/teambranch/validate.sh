#!/usr/bin/env bash
#
# validate.sh - the team-branch validation: piece 6 of integrating to a local
# branch (docs/designs/integrating-to-a-local-branch.md, work item
# yoyodyne-ifd.429.147). It takes one item through the whole flow against a
# scratch Git repository with a team's branch set - run, landed on the project's
# own branch, waiting for the team's merge, its pull request into the team's
# branch recorded, feedback on it, the team's branch moving and the target
# branch brought up to date, the team merging, and the item closing - and prints
# each step by number and name as it passes or fails.
#
#   scripts/teambranch/validate.sh
#
# It needs no outside account and no network. The steps are the Go test beside
# this script, which drives the harness's own pipeline, forge check, update of
# the target branch and run store over real Git; the forge, the tracker and the
# AI provider are the stand-ins the harness's own tests use, so no request
# reaches GitHub or a provider.
#
# Everything is made under one temporary root, which is removed on exit however
# the script ends. HOME is pointed inside it and YOYODYNE_STATE_HOME and
# XDG_STATE_HOME are unset, so the real ~/.yoyodyne is never read or written.
#
# The first eight steps follow one item from its start to its closing, and a
# step that fails stops the ones after it, which are reported as not run. The
# steps after those are cases of their own against a project of their own, so
# one failing hides no other. A step whose piece has not landed is reported as
# not yet built, naming the work it waits on, and counted as neither passed nor
# failed. The script exits non-zero when any step fails or is not run, or the
# test could not be built or run.
#
# Requires go and git. Nothing it starts outlives it: the test runs in the
# foreground and starts no process of its own that keeps running.

set -uo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

for tool in go git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "scripts/teambranch/validate.sh needs $tool" >&2; exit 2; }
done

scratch="$(mktemp -d "${TMPDIR:-/tmp}/yoyodyne-team-branch.XXXXXX")"
scratch="$(cd "$scratch" && pwd)"
cleanup() {
  chmod -R u+rwx "$scratch" 2>/dev/null || true
  rm -rf "$scratch"
}
trap cleanup EXIT

# The Go caches are settled before HOME moves, because Go finds them from HOME.
GOCACHE="${GOCACHE:-$(go env GOCACHE)}"
GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}"
export GOCACHE GOMODCACHE

mkdir -p "$scratch/home" "$scratch/tmp"
export HOME="$scratch/home" TMPDIR="$scratch/tmp"
unset YOYODYNE_STATE_HOME XDG_STATE_HOME
export GIT_CONFIG_NOSYSTEM=1

printf 'The team-branch validation: one item, start to closed, against a scratch repository\n'
printf 'scratch root: %s (removed on exit)\n' "$scratch"

log="$scratch/go-test.log"
(cd "$repository" && YOYODYNE_TEAM_BRANCH_ROOT="$scratch" \
  go test -count=1 -v -timeout 20m -run '^TestTeamBranchFlow$' ./scripts/teambranch) >"$log" 2>&1
code=$?

passed=0 failed=0 notrun=0 unbuilt=0
# The test's own result lines, one per step, in the order they were written.
# Go writes a subtest's name with its spaces as underscores; they are put back
# here so each step reads as its name.
while IFS= read -r line; do
  verdict="$(printf '%s' "$line" | sed -E 's/^ *--- (PASS|FAIL|SKIP): .*/\1/')"
  name="$(printf '%s' "$line" | sed -E 's/^ *--- [A-Z]+: TestTeamBranchFlow\/(.*) \([0-9.]+s\)$/\1/' | tr '_' ' ')"
  case "$verdict" in
    (PASS) passed=$((passed + 1)); printf 'step %s: passed\n' "$name" ;;
    (FAIL)
      key="$(printf '%s' "$line" | sed -E 's/^ *--- FAIL: (TestTeamBranchFlow\/[^ ]*).*/\1/')"
      what="$(awk -v key="=== RUN   $key" '
        $0 == key { on = 1; next }
        on && /^=== RUN/ { exit }
        on && /^ *--- (PASS|FAIL|SKIP):/ { exit }
        on && /^=== (PAUSE|CONT)/ { next }
        on { print "      " $0 }' "$log" | grep -v -E '^ *$' | head -40)"
      if printf '%s' "$what" | grep -q 'not run: step'; then
        notrun=$((notrun + 1)); printf 'step %s: not run, because an earlier step did not happen\n' "$name"
      else
        failed=$((failed + 1)); printf 'step %s: FAILED\n%s\n' "$name" "$what"
      fi
      ;;
    (SKIP)
      unbuilt=$((unbuilt + 1))
      number="${name%% *}"
      why="$(sed -n -E "s/.*not yet built: step $number (waits on .*)/\1/p" "$log" | head -1)"
      printf 'step %s: not yet built, counted neither passed nor failed%s\n' "$name" "${why:+; it $why}"
      ;;
  esac
done < <(grep -E '^ +--- (PASS|FAIL|SKIP): TestTeamBranchFlow/' "$log")

printf '\n%d passed, %d failed, %d not run, %d not yet built\n' "$passed" "$failed" "$notrun" "$unbuilt"

if [ "$((passed + failed + notrun + unbuilt))" -eq 0 ] || { [ "$code" -ne 0 ] && [ "$failed" -eq 0 ]; }; then
  printf 'the steps could not be built or run; go test said:\n' >&2
  tail -40 "$log" >&2
  exit 2
fi
[ "$failed" -eq 0 ] && [ "$notrun" -eq 0 ]
