#!/bin/sh
#
# rerun-failing.sh - run once more only the Go tests a check's first run named
# failing, for `make race` when the harness's checks on main run it a second
# time (internal/maincheck, runFailingAgain).
#
#   sh scripts/rerun-failing.sh GO TIMEOUT [GO TEST FLAG...]
#
# The names are read from YOYODYNE_FAILING_TESTS, one to a line, as the first
# run's output named them: test names such as TestQueue or TestQueue/empty, and
# the packages Go reported failing, such as github.com/example/queue. Each
# package named is run with -count=1, so nothing is read from the test cache,
# and with -run naming only the top-level tests whose declarations are in that
# package's test files. A package none of the named tests is declared in --
# one that panicked or timed out before printing which test failed -- runs
# whole, so a test the names miss is never passed over. Where no package is
# named, the named tests run across the whole module.
#
# Every `go test` started here is bounded by TIMEOUT and run in turn, so
# nothing outlives the script. Exits non-zero when any of them fails.

go=$1
timeout=$2
shift 2

cd "$(dirname "$0")/.." || exit 1

# Names come from test output, so nothing in them is expanded by the shell.
set -f
tests=
packages=
for name in $YOYODYNE_FAILING_TESTS; do
	case $name in
	Test* | Example* | Fuzz*)
		top=${name%%/*}
		case $top in *[!A-Za-z0-9_]*) continue ;; esac
		case " $tests " in *" $top "*) ;; *) tests="$tests $top" ;; esac
		;;
	*)
		case " $packages " in *" $name "*) ;; *) packages="$packages $name" ;; esac
		;;
	esac
done

run() {
	echo "$go test $* -count=1 -timeout $timeout"
	"$go" test "$@" -count=1 -timeout "$timeout"
}

joined() {
	printf '%s\n' "$@" | paste -sd '|' -
}

if [ -z "$packages" ]; then
	if [ -z "$tests" ]; then
		echo "rerun: no failing test or package was named, so there is nothing to run once more" >&2
		exit 1
	fi
	# shellcheck disable=SC2086 # one test name per word
	run "$@" -run "^($(joined $tests))\$" ./...
	exit
fi

failed=0
for package in $packages; do
	dir=$("$go" list -f '{{.Dir}}' "$package" 2>/dev/null) || dir=
	own=
	for test in $tests; do
		# Expansion is on only here, for the package's own test files.
		if [ -n "$dir" ] && (set +f && grep -qs "^func $test(" "$dir"/*_test.go); then
			own="$own $test"
		fi
	done
	if [ -n "$own" ]; then
		# shellcheck disable=SC2086 # one test name per word
		run "$@" -run "^($(joined $own))\$" "$package" || failed=1
	else
		run "$@" "$package" || failed=1
	fi
done
exit $failed
