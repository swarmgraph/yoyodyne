#!/usr/bin/env bash
#
# race-run-test.sh - exercise scripts/race-run.sh against a fabricated module,
# so that splitting internal/orchestrator into parts is shown to run every test
# exactly once rather than asserted to.
#
#   bash scripts/race-run-test.sh
#
# The module holds an internal/orchestrator package with parallel tests, a
# subtest, an example, and one test declared across two lines so the script's
# name reading misses it, and a second package beside it. The script is run
# with a Go command that passes every `go test` on with -v and without -race,
# so the tests compile quickly and every test that ran says so; what is checked
# is which tests ran and how often, and the script's exit status.
#
# Everything lives under one temporary root that is removed on exit. Nothing
# outside that root is written.
#
# Requires bash and go.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
source_root=$(cd "$here/.." && pwd)

root=$(mktemp -d "${TMPDIR:-/tmp}/race-run-test.XXXXXX")
trap 'rm -rf "$root"' EXIT

real_go=$(command -v go)
wrapper="$root/go"
cat >"$wrapper" <<WRAPPER
#!/bin/sh
if [ "\$1" = test ]; then
	shift
	for arg in "\$@"; do
		[ "\$arg" = -race ] || set -- "\$@" "\$arg"
		shift
	done
	exec "$real_go" test -count=1 -v "\$@"
fi
exec "$real_go" "\$@"
WRAPPER
chmod +x "$wrapper"

repo="$root/module"
mkdir -p "$repo/scripts" "$repo/internal/orchestrator" "$repo/other"
cp "$source_root/scripts/race-run.sh" "$repo/scripts/race-run.sh"
printf 'module example.com/fabricated\n\ngo 1.22\n' >"$repo/go.mod"
cat >"$repo/internal/orchestrator/orchestrator_test.go" <<'GO'
package orchestrator

import (
	"fmt"
	"os"
	"testing"
)

func TestAlpha(t *testing.T) {
	t.Parallel()
	t.Run("TestBeta", func(t *testing.T) {})
}
func TestAlphaLonger(t *testing.T) { t.Parallel() }
func TestBeta(t *testing.T)        { t.Parallel() }
func TestDelta(t *testing.T)       { t.Parallel() }
func TestEpsilon(t *testing.T)     {}
func TestGamma(t *testing.T) {
	if os.Getenv("RACE_RUN_TEST_FAIL") != "" {
		t.Fatal("failing on purpose")
	}
}

func TestDeclaredOnTwoLines(
	t *testing.T) {
}

func Example() {
	fmt.Println("example")
	// Output: example
}
GO
cat >"$repo/other/other_test.go" <<'GO'
package other

import "testing"

func TestOther(t *testing.T) {}
GO

failures=0
cases=0

fail() {
	echo "FAIL $1"
	printf '%s\n' "$2" | sed 's/^/    /'
	failures=$((failures + 1))
}

# runs counts the lines in output saying a test started, for one name.
runs() {
	printf '%s\n' "$1" | grep -cE "^=== RUN   $2\$" || true
}

cases=$((cases + 1))
name="three parts run every test, subtest, and example exactly once"
if output=$(cd "$repo" && sh scripts/race-run.sh "$wrapper" 5m 3 ./... 2>&1); then
	bad=
	for test in TestAlpha TestAlpha/TestBeta TestAlphaLonger TestBeta TestDelta TestEpsilon TestGamma TestDeclaredOnTwoLines Example TestOther; do
		[ "$(runs "$output" "$test")" = 1 ] || bad="$bad $test"
	done
	parts=$(printf '%s\n' "$output" | grep -cE '^ok[[:space:]]+example.com/fabricated/internal/orchestrator[[:space:]]' || true)
	if [ -n "$bad" ] || [ "$parts" != 3 ]; then
		fail "$name: not run exactly once:${bad:- (none)}; orchestrator parts that passed: $parts, want 3" "$output"
	else
		echo "ok   $name"
	fi
else
	fail "$name: race-run.sh failed" "$output"
fi

cases=$((cases + 1))
name="a failing test in one part fails the whole run"
if output=$(cd "$repo" && RACE_RUN_TEST_FAIL=1 sh scripts/race-run.sh "$wrapper" 5m 3 ./... 2>&1); then
	fail "$name: race-run.sh passed" "$output"
elif [ "$(runs "$output" TestGamma)" != 1 ]; then
	fail "$name: TestGamma did not run once" "$output"
else
	echo "ok   $name"
fi

cases=$((cases + 1))
name="one part is the single run it replaces"
if output=$(cd "$repo" && sh scripts/race-run.sh "$wrapper" 5m 1 ./... 2>&1) &&
	printf '%s\n' "$output" | grep -qxF "$wrapper test -race -timeout 5m ./..." &&
	! printf '%s\n' "$output" | grep -q 'runs in'; then
	echo "ok   $name"
else
	fail "$name: want one go test over ./..." "$output"
fi

cases=$((cases + 1))
name="packages without internal/orchestrator run as they are named"
if output=$(cd "$repo" && sh scripts/race-run.sh "$wrapper" 5m 3 ./other 2>&1) &&
	printf '%s\n' "$output" | grep -qxF "$wrapper test -race -timeout 5m ./other" &&
	[ "$(runs "$output" TestOther)" = 1 ] && [ "$(runs "$output" TestAlpha)" = 0 ]; then
	echo "ok   $name"
else
	fail "$name: want one go test over ./other" "$output"
fi

if [ "$failures" -ne 0 ]; then
	echo "$failures of $cases cases failed"
	exit 1
fi
echo "all $cases cases passed"
