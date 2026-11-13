#!/usr/bin/env bash
#
# validate.sh - the two-factory validation. It sets up two
# harness homes on one machine, each with its own machine identity and its own
# clone of one throwaway bare remote, and asserts the checks the validation work
# item (yoyodyne-ifd.429.110) names from "What a reviewer can check" in
# docs/designs/working-alongside-other-contributors.md, printing each by number
# and name as it passes or fails.
#
#   scripts/twofactory/validate.sh
#
# It lives beside its Go test, in that package's directory, and has no make
# target, so a change to it is checked as a change to that package alone.
#
# The checks themselves are the Go test beside this script, which drives the
# harness's own code: its claims, its pipeline, its scheduler, its forge check
# and its status line. The forge is the stand-in the harness's own tests use,
# reading and writing the throwaway remote; no request reaches GitHub.
#
# Everything is made under one temporary root, which is removed on exit however
# the script ends. HOME is pointed inside it and YOYODYNE_STATE_HOME and
# XDG_STATE_HOME are unset, so the real ~/.yoyodyne is never read or written.
#
# A check whose piece has not landed is reported as not yet built, naming the
# work it waits on, and counted as neither passed nor failed. The script exits
# non-zero when any check fails, or when the checks could not be built or run.
#
# Requires go and git. Nothing it starts outlives it: the test runs in the
# foreground and starts no process of its own that keeps running.

set -uo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

for tool in go git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "scripts/twofactory/validate.sh needs $tool" >&2; exit 2; }
done

scratch="$(mktemp -d "${TMPDIR:-/tmp}/yoyodyne-two-factories.XXXXXX")"
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

mkdir -p "$scratch/home" "$scratch/tmp" "$scratch/factories"
export HOME="$scratch/home" TMPDIR="$scratch/tmp"
unset YOYODYNE_STATE_HOME XDG_STATE_HOME
export GIT_CONFIG_NOSYSTEM=1

printf 'The two-factory validation: two homes on one machine against one throwaway remote\n'
printf 'scratch root: %s (removed on exit)\n' "$scratch"

log="$scratch/go-test.log"
(cd "$repository" && YOYODYNE_TWO_FACTORY_ROOT="$scratch/factories" \
  go test -count=1 -v -timeout 20m -run '^TestTwoFactories$' ./scripts/twofactory) >"$log" 2>&1
code=$?

passed=0 failed=0 unbuilt=0
# The test's own result lines, one per check, read in the order they were
# written. Go writes a subtest's name with its spaces as underscores; they are
# put back here so each check reads as its name.
while IFS= read -r line; do
  verdict="$(printf '%s' "$line" | sed -E 's/^ *--- (PASS|FAIL|SKIP): .*/\1/')"
  name="$(printf '%s' "$line" | sed -E 's/^ *--- [A-Z]+: TestTwoFactories\/(.*) \([0-9.]+s\)$/\1/' | tr '_' ' ')"
  case "$verdict" in
    (PASS) passed=$((passed + 1)); printf 'check %s: passed\n' "$name" ;;
    (FAIL)
      failed=$((failed + 1)); printf 'check %s: FAILED\n' "$name"
      # What the check said, kept with the check it belongs to.
      key="$(printf '%s' "$line" | sed -E 's/^ *--- FAIL: (TestTwoFactories\/[^ ]*).*/\1/')"
      # Go prints what a check said between its own start and the next
      # check's, and the result lines only once every check has run.
      awk -v key="=== RUN   $key" '
        $0 == key { on = 1; next }
        on && /^=== RUN/ { exit }
        on && /^ *--- (PASS|FAIL|SKIP):/ { exit }
        on && /^=== (PAUSE|CONT)/ { next }
        on { print "      " $0 }' "$log" | grep -v -E '^ *$' | head -40
      ;;
    (SKIP)
      unbuilt=$((unbuilt + 1))
      number="${name%% *}"
      why="$(sed -n -E "s/.*not yet built: check $number (waits on .*)/\1/p" "$log" | head -1)"
      printf 'check %s: not yet built, counted neither passed nor failed%s\n' "$name" "${why:+; it $why}"
      ;;
  esac
done < <(grep -E '^ +--- (PASS|FAIL|SKIP): TestTwoFactories/' "$log")

printf '\n%d passed, %d failed, %d not yet built\n' "$passed" "$failed" "$unbuilt"

if [ "$((passed + failed + unbuilt))" -eq 0 ] || { [ "$code" -ne 0 ] && [ "$failed" -eq 0 ]; }; then
  printf 'the checks could not be built or run; go test said:\n' >&2
  tail -40 "$log" >&2
  exit 2
fi
[ "$failed" -eq 0 ]
