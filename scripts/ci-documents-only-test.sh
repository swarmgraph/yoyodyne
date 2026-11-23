#!/usr/bin/env bash
#
# ci-documents-only-test.sh - run the first step of every job in
# .github/workflows/ci.yml against a stub of GitHub's compare API, so which
# changes let the jobs pass without running is executed rather than asserted.
#
#   bash scripts/ci-documents-only-test.sh
#
# The step's script is the text between its "documents-only: begin" and
# "documents-only: end" lines, taken from the workflow as it is checked in and
# unindented the way GitHub reads a block scalar. Each case gives it an event,
# the paths the stub `gh` answers with (or a failure), and checks the `only`
# it writes to GITHUB_OUTPUT.
#
# Everything lives under one temporary root that is removed on exit. Nothing
# outside that root is written.
#
# Requires bash, sed and awk.

set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
workflow="$here/../.github/workflows/ci.yml"

root=$(mktemp -d "${TMPDIR:-/tmp}/ci-documents-only-test.XXXXXX")
trap 'rm -rf "$root"' EXIT

# The step as GitHub runs it: the lines between the markers, with the block
# scalar's indentation removed.
step="$root/step.sh"
awk '/# documents-only: begin/{on=1} on{print} /# documents-only: end/{exit}' "$workflow" |
	sed 's/^          //' >"$step"
if ! grep -q 'documents-only: end' "$step"; then
	echo "FAIL: no documents-only step between its markers in $workflow" >&2
	exit 1
fi

# The stub answers `gh api` with the paths in $STUB_PATHS, one per line, or
# fails when STUB_FAIL is set, and records what it was asked.
mkdir -p "$root/bin"
cat >"$root/bin/gh" <<'STUB'
#!/bin/sh
echo "$*" >"$STUB_ROOT/asked"
if [ -n "${STUB_FAIL:-}" ]; then
	echo "gh: HTTP 502" >&2
	exit 1
fi
[ -n "$STUB_PATHS" ] && printf '%s\n' "$STUB_PATHS"
exit 0
STUB
chmod +x "$root/bin/gh"
export STUB_ROOT="$root"

failures=0
cases=0

# check NAME EVENT WANT PATHS [fail]
check() {
	local name=$1 event=$2 want=$3 paths=$4 fail=${5:-}
	cases=$((cases + 1))
	local output="$root/output-$cases"
	: >"$output"
	rm -f "$root/asked"
	local log
	if ! log=$(env PATH="$root/bin:$PATH" GITHUB_OUTPUT="$output" GITHUB_EVENT_NAME="$event" \
		GITHUB_REPOSITORY=owner/repo PR_BASE=base1 PR_HEAD=head1 GROUP_BASE=base2 GROUP_HEAD=head2 \
		STUB_PATHS="$paths" STUB_FAIL="$fail" bash "$step" 2>&1); then
		echo "FAIL: $name: the step itself failed, which would fail the job:" >&2
		echo "$log" >&2
		failures=$((failures + 1))
		return
	fi
	local got
	got=$(sed -n 's/^only=//p' "$output")
	if [ "$got" != "$want" ]; then
		echo "FAIL: $name: only=$got, want $want; the step said:" >&2
		echo "$log" >&2
		failures=$((failures + 1))
	fi
}

docs="docs/designs/a.md
docs/product/goals/v1-goals.md
docs/decisions/invariants/b.md
docs/decisions/README.md"

check "documents in the three homes, on a pull request" pull_request true "$docs"
check "documents in the three homes, in a merge group" merge_group true "$docs"
if ! grep -q 'compare/base2...head2' "$root/asked"; then
	echo "FAIL: a merge group asked for $(cat "$root/asked"), not its own base and head" >&2
	failures=$((failures + 1))
fi
check "a push to main runs in full" push false "$docs"
check "a code change runs in full" pull_request false "docs/designs/a.md
internal/cli/cli.go"
check "a document outside the three homes runs in full" pull_request false "docs/developing-yoyo.md"
check "a file in a home that is not Markdown runs in full" pull_request false "docs/designs/diagram.png"
check "a README outside the homes runs in full" pull_request false "README.md"
check "a path that only starts like a home runs in full" pull_request false "docs/productivity/a.md"
check "the API failing runs in full" pull_request false "" fail
check "an empty list runs in full" pull_request false ""

many=$(for i in $(seq 1 300); do echo "docs/designs/d$i.md"; done)
check "300 paths, the most the API lists, runs in full" pull_request false "$many"

if [ "$failures" -ne 0 ]; then
	echo "$failures of $cases cases failed" >&2
	exit 1
fi
echo "ci-documents-only-test: all $cases cases passed"
