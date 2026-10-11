#!/bin/sh
#
# race-skip.sh - say whether `make race` can be skipped for the change in this
# checkout, because nothing it touches can affect Go code.
#
#   sh scripts/race-skip.sh
#
# Exits 0, having printed why, when the race check can be skipped. Exits
# non-zero when it has to run, which is every case it cannot prove otherwise:
# so the Makefile runs the race detector on anything but a clear 0, and a
# failure of this script costs time rather than a check.
#
# The change is everything between the merge base with the target branch and
# the working tree: what is committed on this branch, what is staged or not,
# and files git does not track yet that it would not ignore. The target branch
# is RACE_BASE when that is set, and otherwise origin/main, then main. With no
# such branch -- a shallow CI checkout -- there is no merge base, and the check
# runs.
#
# The check is skipped only when every changed file is a Markdown file or one of
# the tracker's exports (.beads/issues.jsonl, .beads/interactions.jsonl), and
# none of them is inside a directory that holds a .go file, at any depth above
# it. A Markdown file in a Go package's directory may be embedded, and the
# package's testdata, fixtures, and embedded files are read by its tests, so any
# of those runs the check; so does go.mod, go.sum, the Makefile, and anything
# else. A change that touches nothing runs it too: that is a person running
# `make race` on the target branch, or a landing over a commit already on it,
# and both want the whole suite.

# Paths are compared from the top of the checkout, which is where git names them.
top=$(git rev-parse --show-toplevel 2>/dev/null) && cd "$top" || {
	echo "race: not in a git checkout, so the change cannot be told apart and the race check runs"
	exit 1
}

if [ -n "${RACE_BASE:-}" ]; then
	candidates=$RACE_BASE
else
	candidates="origin/main main"
fi
base=
for candidate in $candidates; do
	if base=$(git merge-base HEAD "$candidate" 2>/dev/null); then
		target=$candidate
		break
	fi
	base=
done
if [ -z "$base" ]; then
	echo "race: no merge base with $candidates, so the change cannot be told apart and the race check runs"
	exit 1
fi

tracked=$(git -c core.quotePath=false diff --name-only --no-renames "$base" --) &&
	untracked=$(git -c core.quotePath=false ls-files --others --exclude-standard) || {
	echo "race: could not list what changed since $target, so the race check runs"
	exit 1
}
changed=$(printf '%s\n%s\n' "$tracked" "$untracked" | sed '/^$/d' | sort -u)
if [ -z "$changed" ]; then
	echo "race: nothing has changed since the merge base with $target, so the whole race check runs"
	exit 1
fi

# holds_go reports whether a directory, or any directory above it up to the
# root of the checkout, holds a .go file.
holds_go() {
	dir=$1
	while :; do
		for file in "$dir"/*.go; do
			[ -e "$file" ] && return 0
		done
		[ "$dir" = . ] && return 1
		case "$dir" in
		*/*) dir=${dir%/*} ;;
		*) dir=. ;;
		esac
	done
}

count=0
while IFS= read -r path; do
	case "$path" in
	*.md | .beads/issues.jsonl | .beads/interactions.jsonl) ;;
	*)
		echo "race: the change touches $path, so the race check runs"
		exit 1
		;;
	esac
	case "$path" in
	*/*) dir=${path%/*} ;;
	*) dir=. ;;
	esac
	if holds_go "$dir"; then
		echo "race: the change touches $path, beside or under Go source, so the race check runs"
		exit 1
	fi
	count=$((count + 1))
done <<EOF
$changed
EOF

echo "race: skipped, because all $count files this change touches since its merge base with $target are Markdown or tracker exports outside every Go package's directory"
exit 0
