#!/usr/bin/env bash
#
# check-scope-test.sh - exercise `make test` and `make race` given the packages
# a change touches as CHANGED_GO_PACKAGES, against fabricated repositories, so
# which packages each check runs over is executed rather than asserted.
#
#   bash scripts/check-scope-test.sh
#
# Each case builds a scratch repository holding a copy of this repository's
# Makefile, scripts/check-scope.sh and scripts/race-skip.sh, two Go packages
# with testdata, documents inside and outside them, and the tracker's export,
# all committed on main. It then makes one change on a branch and runs the
# target with a stub Go command that records the packages the suite was asked
# to run over, so what is checked is the target's decision and the line it
# prints, and no Go is compiled.
#
# Everything lives under one temporary root that is removed on exit. Nothing
# outside that root is written.
#
# Requires bash, git, and make.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
source_root=$(cd "$here/.." && pwd)

root=$(mktemp -d "${TMPDIR:-/tmp}/check-scope-test.XXXXXX")
trap 'rm -rf "$root"' EXIT

# The stub stands in for the Go command: `go env GOCACHE` names a writable
# cache for the Makefile's cachecheck, a `go test -run` -- the named tests
# `make test` runs after its suite -- prints the lines those steps look for,
# and any other `go test` writes the packages it was given to a marker.
stub="$root/go"
cat >"$stub" <<'STUB'
#!/bin/sh
case "$1" in
env) echo "$STUB_ROOT/cache" ;;
test)
	shift
	for arg in "$@"; do
		if [ "$arg" = -run ]; then
			echo "shipped documentation is within its margin"
			echo "--- PASS: TestThePageRendersEverySectionInEveryState (0.00s)"
			exit 0
		fi
	done
	packages=
	while [ $# -gt 0 ]; do
		case "$1" in
		-timeout) shift ;;
		-*) ;;
		*) packages="$packages $1" ;;
		esac
		shift
	done
	echo "stub go test over${packages}"
	echo "${packages# }" >"$STUB_ROOT/ran"
	;;
esac
STUB
chmod +x "$stub"
export STUB_ROOT="$root"

# Make runs here as a make of its own, not one joining whoever started us.
export MAKEFLAGS= MFLAGS= MAKELEVEL=0
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com
unset RACE_BASE RACE_PACKAGES CHANGED_GO_PACKAGES

failures=0
cases=0

# fabricate makes a fresh repository with main committed and a branch checked
# out, and prints its path.
fabricate() {
	local repo="$root/repo-$cases"
	mkdir -p "$repo/scripts" "$repo/pkg/testdata" "$repo/other" "$repo/docs" "$repo/.beads"
	cp "$source_root/Makefile" "$repo/Makefile"
	cp "$source_root/scripts/check-scope.sh" "$repo/scripts/check-scope.sh"
	cp "$source_root/scripts/race-skip.sh" "$repo/scripts/race-skip.sh"
	printf 'module example.com/fabricated\n\ngo 1.22\n' >"$repo/go.mod"
	printf 'package pkg\n' >"$repo/pkg/pkg.go"
	printf 'fixture\n' >"$repo/pkg/testdata/input.txt"
	printf '# Notes\n' >"$repo/pkg/notes.md"
	printf 'package other\n' >"$repo/other/other.go"
	printf '# Guide\n' >"$repo/docs/guide.md"
	printf '{}\n' >"$repo/.beads/issues.jsonl"
	git -C "$repo" init -q -b main
	git -C "$repo" add -A
	git -C "$repo" commit -q -m base
	git -C "$repo" checkout -q -b work
	printf '%s\n' "$repo"
}

# expect runs a target in a repository and checks the packages the suite ran
# over -- "none" where it did not run -- and that the line saying why was
# printed.
expect() {
	local name=$1 target=$2 want=$3 line=$4
	shift 4
	rm -f "$root/ran"
	local output ran=none
	if ! output=$(cd "$repo" && make --no-print-directory "$target" GO="$stub" "$@" 2>&1); then
		echo "FAIL $name: make $target failed:"
		printf '%s\n' "$output" | sed 's/^/    /'
		failures=$((failures + 1))
		return
	fi
	[ -e "$root/ran" ] && ran=$(cat "$root/ran")
	if [ "$ran" != "$want" ]; then
		echo "FAIL $name: make $target ran over \"$ran\", want \"$want\"; make printed:"
		printf '%s\n' "$output" | sed 's/^/    /'
		failures=$((failures + 1))
	elif ! grep -qF -- "$line" <<<"$output"; then
		echo "FAIL $name: make $target did not print \"$line\"; it printed:"
		printf '%s\n' "$output" | sed 's/^/    /'
		failures=$((failures + 1))
	else
		echo "ok   $name"
	fi
}

next() {
	cases=$((cases + 1))
	repo=$(fabricate)
}

next
printf 'package pkg\n\nvar X = 1\n' >"$repo/pkg/pkg.go"
expect "a change to one package tests that package" test ./pkg \
	"test: runs over ./pkg, the packages holding the 1 files this change touches" CHANGED_GO_PACKAGES=./pkg
expect "a change to one package races that package" race ./pkg \
	"race: runs over ./pkg, the packages holding the 1 files this change touches" CHANGED_GO_PACKAGES=./pkg

next
printf 'changed fixture\n' >"$repo/pkg/testdata/input.txt"
printf '# Notes, revised\n' >"$repo/pkg/notes.md"
printf '{"id":"x"}\n' >"$repo/.beads/issues.jsonl"
git -C "$repo" commit -q -am fixtures
expect "testdata, a package's Markdown and the tracker export stay in the package" test ./pkg \
	"test: runs over ./pkg, the packages holding the 3 files" CHANGED_GO_PACKAGES=./pkg

next
printf 'package pkg\n\nvar X = 1\n' >"$repo/pkg/pkg.go"
printf 'package other\n\nvar Y = 1\n' >"$repo/other/other.go"
expect "a change to two packages tests both" test "./other ./pkg" \
	"test: runs over ./other ./pkg" CHANGED_GO_PACKAGES="./other ./pkg"

next
printf 'package pkg\n\nvar X = 1\n' >"$repo/pkg/pkg.go"
printf '# Guide, revised\n' >"$repo/docs/guide.md"
expect "a document outside every package tests the whole module" test ./... \
	"test: the change touches docs/guide.md, outside every Go package, which the checks read, so the whole module runs" CHANGED_GO_PACKAGES=./pkg
expect "a document outside every package races only the package" race ./pkg \
	"race: runs over ./pkg, the packages holding the 2 files" CHANGED_GO_PACKAGES=./pkg

next
printf '# Guide, revised\n' >"$repo/docs/guide.md"
expect "an empty scope over documents races nothing and says so" race none \
	"race: the change touches no Go package, and none of the 1 files it touches since its merge base with main is one this check reads, so there is no package to run it over" CHANGED_GO_PACKAGES=
expect "an empty scope over documents tests the whole module" test ./... \
	"test: the change touches docs/guide.md, outside every Go package, which the checks read, so the whole module runs" CHANGED_GO_PACKAGES=

next
printf '\n' >>"$repo/Makefile"
expect "an empty scope over the Makefile races the whole module" race ./... \
	"race: the change touches Makefile, outside every Go package, which the checks read, so the whole module runs" CHANGED_GO_PACKAGES=
expect "a scope beside a Makefile change tests the whole module" test ./... \
	"test: the change touches Makefile, outside every Go package, which the checks read, so the whole module runs" CHANGED_GO_PACKAGES=./pkg

next
printf 'package pkg\n\nvar X = 1\n' >"$repo/pkg/pkg.go"
expect "a scope that leaves out a changed package runs the whole module" race ./... \
	"race: the change touches pkg/pkg.go, in ./pkg, which the packages it was given (./other) do not name, so the whole module runs" CHANGED_GO_PACKAGES=./other

next
printf 'package pkg\n\nvar X = 1\n' >"$repo/pkg/pkg.go"
git -C "$repo" branch -q -m main elsewhere
expect "no target branch to compare with runs the whole module" test ./... \
	"test: no merge base with origin/main main to read the change from, so the whole module runs" CHANGED_GO_PACKAGES=./pkg

next
printf '# Guide, revised\n' >"$repo/docs/guide.md"
expect "the whole module over documents alone is skipped by the race check" race none \
	"race: skipped, because all 1 files this change touches" CHANGED_GO_PACKAGES=./...
expect "the whole module tests the whole module" test ./... \
	"test: the harness asked for the whole module" CHANGED_GO_PACKAGES=./...

next
printf 'package pkg\n\nvar X = 1\n' >"$repo/pkg/pkg.go"
expect "the whole module over a Go change races the whole module" race ./... \
	"race: the harness asked for the whole module" CHANGED_GO_PACKAGES=./...

next
printf 'package pkg\n\nvar X = 1\n' >"$repo/pkg/pkg.go"
expect "nothing named tests the whole module" test ./... \
	"stub go test over ./..."
expect "naming the race packages runs exactly those" race ./other \
	"stub go test over ./other" RACE_PACKAGES=./other CHANGED_GO_PACKAGES=./pkg

if [ "$failures" -ne 0 ]; then
	echo "$failures of $cases cases failed"
	exit 1
fi
echo "all $cases cases passed"
