#!/bin/sh
#
# race-run.sh - run the race detector over the packages `make race` decided on,
# with internal/orchestrator's tests split into parts that run side by side.
#
#   sh scripts/race-run.sh GO TIMEOUT PARTS PACKAGE...
#
# GO is the Go command, TIMEOUT is go test's -timeout for every invocation, and
# PARTS is how many parts internal/orchestrator is split into. The PACKAGE
# arguments are what `make race` would have handed one `go test -race`.
#
# internal/orchestrator is by far the slowest package under the race detector:
# one test binary of it has taken 40 minutes and more on a loaded machine,
# against a go test limit of 43. Nearly every one of its tests is parallel, so
# what one binary can do at once is capped by its -parallel figure, and much of
# what its tests wait on is Git and other processes rather than the CPU. Split
# across several binaries it finishes in a fraction of the time
# (docs/developing-yoyo.md has the figures), so no one `go test` comes near its
# limit.
#
# Each part but the last runs, with -run, the top-level tests whose names fall
# to it; the names are read from the package's test files and dealt out in
# turn. The last part runs, with -skip, everything the other parts do not name.
# So every test, example, and fuzz seed the package had run before still runs
# exactly once, including any whose declaration the name reading here missed:
# such a test lands in the last part rather than nowhere. The rest of the
# packages run together in one `go test`, beside the parts.
#
# Where internal/orchestrator is not among the packages, PARTS is below 2, or
# the packages cannot be listed, this runs the one `go test -race` it always
# did. Exits non-zero when any invocation fails.
#
# Everything started here is a `go test` bounded by TIMEOUT, and the script
# waits for all of them, so nothing it starts outlives it.

go=$1
timeout=$2
parts=$3
shift 3

# The Makefile runs from the top of the checkout, and the test files are read
# from there.
cd "$(dirname "$0")/.." || exit 1

whole() {
	[ $# -gt 0 ] || return 0
	echo "$go test -race -timeout $timeout $*"
	"$go" test -race -timeout "$timeout" "$@"
}

case $parts in
'' | *[!0-9]*) parts=1 ;;
esac
split=./internal/orchestrator
if [ "$parts" -lt 2 ] || [ $# -eq 0 ]; then
	whole "$@"
	exit
fi

target=$("$go" list "$split" 2>/dev/null) || target=
listed=$("$go" list "$@" 2>/dev/null) || listed=
names=$(sed -n 's/^func \(Test[A-Za-z0-9_]*\)([A-Za-z_][A-Za-z0-9_]* \*testing\.T).*/\1/p' "$split"/*_test.go 2>/dev/null | LC_ALL=C sort -u)
if [ -z "$target" ] || [ -z "$names" ] || ! printf '%s\n' "$listed" | grep -qxF "$target"; then
	whole "$@"
	exit
fi
rest=$(printf '%s\n' "$listed" | grep -vxF "$target")

# pattern prints an anchored -run or -skip pattern of the names whose place in
# the sorted list, counted from 0, is (or with "not", is not) the part given.
pattern() {
	printf '%s\n' "$names" | awk -v parts="$parts" -v part="$1" -v want="$2" '
		((NR - 1) % parts == part) == (want == "is") { joined = joined (joined == "" ? "" : "|") $0 }
		END { printf "^(%s)$", joined }'
}

count=$(printf '%s\n' "$names" | wc -l | tr -d ' ')
echo "race: internal/orchestrator runs in $parts parts side by side, its $count tests dealt out by name"

pids=
if [ -n "$rest" ]; then
	echo "$go test -race -timeout $timeout <$(printf '%s\n' "$rest" | wc -l | tr -d ' ') packages besides internal/orchestrator>"
	# shellcheck disable=SC2086 # one import path per word
	"$go" test -race -timeout "$timeout" $rest &
	pids="$pids $!"
fi
part=0
last=$((parts - 1))
while [ "$part" -lt "$last" ]; do
	echo "$go test -race -timeout $timeout -run '<part $((part + 1)) of $parts: test names $((part + 1)), $((part + 1 + parts)), $((part + 1 + 2 * parts)), ...>' $split"
	"$go" test -race -timeout "$timeout" -run "$(pattern "$part" is)" "$split" &
	pids="$pids $!"
	part=$((part + 1))
done
echo "$go test -race -timeout $timeout -skip '<part $parts of $parts: every name the other parts run>' $split"
"$go" test -race -timeout "$timeout" -skip "$(pattern "$last" not)" "$split" &
pids="$pids $!"

failed=0
for pid in $pids; do
	wait "$pid" || failed=1
done
exit $failed
