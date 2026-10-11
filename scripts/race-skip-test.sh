#!/usr/bin/env bash
#
# race-skip-test.sh - exercise `make race` and scripts/race-skip.sh against
# fabricated repositories, so which changes skip the race check is executed
# rather than asserted.
#
#   bash scripts/race-skip-test.sh
#
# Each case builds a scratch repository holding a copy of this repository's
# Makefile and scripts/race-skip.sh, a Go package with testdata, documents
# inside and outside it, and the tracker's export, all committed on main. It
# then makes one change on a branch and runs `make race` with a stub Go command
# that records whether the race detector was asked for, so what is checked is
# the target's decision and the line it prints, and no Go is compiled.
#
# Everything lives under one temporary root that is removed on exit. Nothing
# outside that root is written.
#
# Requires bash, git, and make.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
source_root=$(cd "$here/.." && pwd)

root=$(mktemp -d "${TMPDIR:-/tmp}/race-skip-test.XXXXXX")
trap 'rm -rf "$root"' EXIT

# The stub stands in for the Go command: `go env GOCACHE` names a writable
# cache for the Makefile's cachecheck, and `go test -race` leaves a marker.
stub="$root/go"
cat >"$stub" <<'STUB'
#!/bin/sh
case "$1" in
env) echo "$STUB_ROOT/cache" ;;
test) echo "stub go $*"; : >"$STUB_ROOT/raced" ;;
esac
STUB
chmod +x "$stub"
export STUB_ROOT="$root"

# Make runs here as a make of its own, not one joining whoever started us.
export MAKEFLAGS= MFLAGS= MAKELEVEL=0
export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_NOSYSTEM=1
export GIT_AUTHOR_NAME=test GIT_AUTHOR_EMAIL=test@example.com
export GIT_COMMITTER_NAME=test GIT_COMMITTER_EMAIL=test@example.com
unset RACE_BASE RACE_PACKAGES

failures=0
cases=0

# fabricate makes a fresh repository with main committed and a branch checked
# out, and prints its path.
fabricate() {
	local repo="$root/repo-$cases"
	mkdir -p "$repo/scripts" "$repo/pkg/testdata" "$repo/docs" "$repo/.beads"
	cp "$source_root/Makefile" "$repo/Makefile"
	cp "$source_root/scripts/race-skip.sh" "$repo/scripts/race-skip.sh"
	printf 'module example.com/fabricated\n\ngo 1.22\n' >"$repo/go.mod"
	printf 'package pkg\n' >"$repo/pkg/pkg.go"
	printf 'fixture\n' >"$repo/pkg/testdata/input.txt"
	printf '# Notes\n' >"$repo/pkg/notes.md"
	printf '# Guide\n' >"$repo/docs/guide.md"
	printf '# Readme\n' >"$repo/README.md"
	printf '{}\n' >"$repo/.beads/issues.jsonl"
	git -C "$repo" init -q -b main
	git -C "$repo" add -A
	git -C "$repo" commit -q -m base
	git -C "$repo" checkout -q -b work
	printf '%s\n' "$repo"
}

# expect runs `make race` in a repository and checks whether the race detector
# ran, and that the line saying why was printed.
expect() {
	local name=$1 repo=$2 want=$3 line=$4
	shift 4
	rm -f "$root/raced"
	local output ran=no
	if ! output=$(cd "$repo" && make --no-print-directory race GO="$stub" "$@" 2>&1); then
		echo "FAIL $name: make race failed:"
		printf '%s\n' "$output" | sed 's/^/    /'
		failures=$((failures + 1))
		return
	fi
	[ -e "$root/raced" ] && ran=yes
	if [ "$ran" != "$want" ]; then
		echo "FAIL $name: race detector ran=$ran, want $want; make printed:"
		printf '%s\n' "$output" | sed 's/^/    /'
		failures=$((failures + 1))
	elif ! grep -qF -- "$line" <<<"$output"; then
		echo "FAIL $name: make did not print \"$line\"; it printed:"
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
printf '# Guide, revised\n' >"$repo/docs/guide.md"
printf '# Readme, revised\n' >"$repo/README.md"
printf '{"id":"x"}\n' >"$repo/.beads/issues.jsonl"
printf '{}\n' >"$repo/.beads/interactions.jsonl"
git -C "$repo" add docs/guide.md && git -C "$repo" commit -q -m docs
printf '# New\n' >"$repo/docs/new.md"
expect "a change to documents and tracker exports alone skips" "$repo" no \
	"race: skipped, because all 5 files this change touches since its merge base with main are Markdown or tracker exports outside every Go package's directory"

next
printf 'changed fixture\n' >"$repo/pkg/testdata/input.txt"
git -C "$repo" commit -q -am testdata
expect "a change under a Go package's testdata runs" "$repo" yes \
	"race: the change touches pkg/testdata/input.txt, so the race check runs"

next
printf 'package pkg\n\nvar X = 1\n' >"$repo/pkg/pkg.go"
expect "a change to a Go file runs" "$repo" yes \
	"race: the change touches pkg/pkg.go, so the race check runs"

next
printf '# Notes, revised\n' >"$repo/pkg/notes.md"
expect "a Markdown file in a Go package's directory runs" "$repo" yes \
	"race: the change touches pkg/notes.md, beside or under Go source, so the race check runs"

next
mkdir -p "$repo/pkg/docs"
printf '# Deeper\n' >"$repo/pkg/docs/deeper.md"
expect "a Markdown file below a Go package's directory runs" "$repo" yes \
	"race: the change touches pkg/docs/deeper.md, beside or under Go source, so the race check runs"

next
printf 'module example.com/fabricated\n\ngo 1.23\n' >"$repo/go.mod"
printf '# Guide, revised\n' >"$repo/docs/guide.md"
expect "go.mod beside documents runs" "$repo" yes \
	"race: the change touches go.mod, so the race check runs"

next
printf '{}\n' >"$repo/.beads/other.jsonl"
expect "a tracker file that is not one of its exports runs" "$repo" yes \
	"race: the change touches .beads/other.jsonl, so the race check runs"

next
expect "a change that touches nothing runs the whole suite" "$repo" yes \
	"race: nothing has changed since the merge base with main, so the whole race check runs"

next
printf '# Guide, revised\n' >"$repo/docs/guide.md"
git -C "$repo" branch -q -m main elsewhere
expect "no target branch to compare with runs" "$repo" yes \
	"race: no merge base with origin/main main, so the change cannot be told apart and the race check runs"

next
printf '# Guide, revised\n' >"$repo/docs/guide.md"
expect "naming the packages runs them whatever changed" "$repo" yes \
	"stub go test -race" RACE_PACKAGES=./...

next
git -C "$repo" mv docs/guide.md pkg/guide.md
expect "a document moved into a Go package's directory runs" "$repo" yes \
	"race: the change touches pkg/guide.md, beside or under Go source, so the race check runs"

if [ "$failures" -ne 0 ]; then
	echo "$failures of $cases cases failed"
	exit 1
fi
echo "all $cases cases passed"
