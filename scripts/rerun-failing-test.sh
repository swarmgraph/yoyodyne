#!/usr/bin/env bash
#
# rerun-failing-test.sh - exercise scripts/rerun-failing.sh against a
# fabricated module, so that running a check's failing tests once more is shown
# to run only those tests, and a package whose failing test was not named
# whole, rather than asserted to.
#
#   bash scripts/rerun-failing-test.sh
#
# The module holds two packages. The script is run with a Go command that
# passes every `go test` on with -v and without -race, so the tests compile
# quickly and every test that ran says so; what is checked is which tests ran
# and the script's exit status.
#
# Everything lives under one temporary root that is removed on exit. Nothing
# outside that root is written.
#
# Requires bash and go.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
source_root=$(cd "$here/.." && pwd)

root=$(mktemp -d "${TMPDIR:-/tmp}/rerun-failing-test.XXXXXX")
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
	exec "$real_go" test -v "\$@"
fi
exec "$real_go" "\$@"
WRAPPER
chmod +x "$wrapper"

repo="$root/module"
mkdir -p "$repo/scripts" "$repo/queue" "$repo/store"
cp "$source_root/scripts/rerun-failing.sh" "$repo/scripts/rerun-failing.sh"
printf 'module example.com/fabricated\n\ngo 1.22\n' >"$repo/go.mod"
cat >"$repo/queue/queue_test.go" <<'GO'
package queue

import (
	"os"
	"testing"
)

func TestQueue(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if os.Getenv("RERUN_TEST_FAIL") != "" {
			t.Fatal("failing on purpose")
		}
	})
}
func TestQueueOther(t *testing.T) {}
GO
cat >"$repo/store/store_test.go" <<'GO'
package store

import "testing"

func TestStoreOne(t *testing.T) {}
func TestStoreTwo(t *testing.T) {}
GO

failures=0
fail() {
	echo "FAIL: $*" >&2
	failures=$((failures + 1))
}

ran() {
	grep -c "^=== RUN   $1\$" "$2" || true
}

# The named test runs, alone in its own package; the other package, named
# failing with no test of its own named, runs whole.
log="$root/one.log"
YOYODYNE_FAILING_TESTS='TestQueue/empty
example.com/fabricated/queue
example.com/fabricated/store' sh "$repo/scripts/rerun-failing.sh" "$wrapper" 1m -race >"$log" 2>&1 ||
	fail "a passing rerun exited non-zero: $(cat "$log")"
[ "$(ran TestQueue "$log")" = 1 ] || fail "TestQueue did not run once: $(cat "$log")"
[ "$(ran TestQueueOther "$log")" = 0 ] || fail "TestQueueOther ran, though it was not named"
[ "$(ran TestStoreOne "$log")" = 1 ] || fail "the store package, named with no test, did not run whole"
[ "$(ran TestStoreTwo "$log")" = 1 ] || fail "the store package, named with no test, did not run whole"
grep -q -- '-count=1' "$log" || fail "the rerun could have read the test cache: $(cat "$log")"

# A test that fails again fails the script.
log="$root/two.log"
if RERUN_TEST_FAIL=1 YOYODYNE_FAILING_TESTS='TestQueue
example.com/fabricated/queue' sh "$repo/scripts/rerun-failing.sh" "$wrapper" 1m -race >"$log" 2>&1; then
	fail "a test that failed again passed the script"
fi

# Names are never expanded by the shell, and names that are no test are
# dropped: with nothing left to run, the script says so and fails.
log="$root/three.log"
if YOYODYNE_FAILING_TESTS='Test*' sh "$repo/scripts/rerun-failing.sh" "$wrapper" 1m >"$log" 2>&1; then
	fail "a rerun with nothing to run passed"
fi
grep -q 'nothing to run once more' "$log" || fail "a rerun with nothing to run did not say so: $(cat "$log")"

if [ "$failures" -ne 0 ]; then
	exit 1
fi
echo "rerun-failing-test: ok"
