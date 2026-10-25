#!/bin/sh
#
# check-scope.sh - say which Go packages `make test` or `make race` runs over,
# given the packages the harness says the change touches.
#
#   sh scripts/check-scope.sh test|race "<packages>"
#
# The packages are what the harness hands every check as
# YOYODYNE_CHANGED_GO_PACKAGES: "./dir" patterns, "./..." for the whole module,
# or nothing for a change that touches no Go package. The Makefile passes them
# on as CHANGED_GO_PACKAGES.
#
# It prints the packages to run on standard output -- the patterns, "./...", or
# an empty line for none -- and why on standard error, and exits 0. It exits
# non-zero only when it could not decide, and the Makefile then runs the whole
# module: every doubt costs time rather than a check.
#
# The harness's list names the packages that hold a changed file, and nothing
# about the files outside every package, which the checks here read too: the
# tests read the documents, the Makefile, the scripts, and the configuration,
# and nothing narrower than the whole suite knows which test reads which file.
# So the list is used only when the change, read from Git, agrees with it:
#
#   - "./..." runs the whole module. For the race check, the whole module is
#     first put to scripts/race-skip.sh, which skips it for a change that is
#     only Markdown and tracker exports outside every Go package's directory,
#     exactly as `make race` with nothing named does.
#   - A list runs those packages, unless the change touches a file outside every
#     package that the check reads, or a package the list does not name, or Git
#     cannot say what changed. Then the whole module runs. For `test`, any file
#     outside every package counts except the tracker's exports; for `race`,
#     Markdown outside every Go package's directory does not count either, which
#     is the rule scripts/race-skip.sh already applies.
#   - Nothing is a change that touches no Go package. The race check runs over
#     nothing, and says so, when the change touches only files the race check
#     does not read; otherwise the whole module runs. `test` reads every file
#     outside the packages, so it runs the whole module for any such change.
#
# The change is everything between the merge base with the target branch and
# the working tree, as scripts/race-skip.sh reads it: committed, staged or not,
# and files git does not track yet that it would not ignore. The target branch
# is RACE_BASE when that is set, and otherwise origin/main, then main.
#
# A file belongs to the nearest directory at or above it that holds a .go file,
# skipping a directory named testdata or one whose name begins with "." or "_",
# which is how the harness works out its list (internal/checks/narrow.go).

kind=$1
given=$2
case "$kind" in
test | race) ;;
*)
	echo "check-scope: the first argument is test or race, not \"$kind\"" >&2
	exit 2
	;;
esac

say() {
	echo "$kind: $*" >&2
}

whole() {
	say "$*, so the whole module runs"
	echo "./..."
	exit 0
}

# Paths are compared from the top of the checkout, which is where git names them.
top=$(git rev-parse --show-toplevel 2>/dev/null) && cd "$top" || {
	say "not in a git checkout, so the change cannot be told apart"
	echo "./..."
	exit 0
}

given=$(printf '%s\n' "$given" | tr -s ' \t' '  ' | sed 's/^ //; s/ $//')

if [ "$given" = "./..." ]; then
	if [ "$kind" = race ]; then
		if skipped=$(sh scripts/race-skip.sh); then
			echo "$skipped" >&2
			echo ""
			exit 0
		fi
		echo "$skipped" >&2
	fi
	say "the harness asked for the whole module"
	echo "./..."
	exit 0
fi

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
[ -n "$base" ] || whole "no merge base with $candidates to read the change from"

tracked=$(git -c core.quotePath=false diff --name-only --no-renames "$base" --) &&
	untracked=$(git -c core.quotePath=false ls-files --others --exclude-standard) ||
	whole "could not list what changed since $target"
changed=$(printf '%s\n%s\n' "$tracked" "$untracked" | sed '/^$/d' | sort -u)

# package_of prints the package a path belongs to, as a "./dir" pattern, or
# nothing for a path above every package.
package_of() {
	case "$1" in
	*/*) dir=${1%/*} ;;
	*) dir=. ;;
	esac
	while [ "$dir" != . ]; do
		case "${dir##*/}" in
		testdata | .* | _*) ;;
		*)
			for file in "$dir"/*.go; do
				if [ -e "$file" ]; then
					echo "./$dir"
					return
				fi
			done
			;;
		esac
		case "$dir" in
		*/*) dir=${dir%/*} ;;
		*) dir=. ;;
		esac
	done
	for file in ./*.go; do
		if [ -e "$file" ]; then
			echo "."
			return
		fi
	done
}

# holds_go reports whether a directory, or any directory above it up to the
# root of the checkout, holds a .go file: scripts/race-skip.sh's test for a
# Markdown file the race check may read.
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
	[ -n "$path" ] || continue
	count=$((count + 1))
	package=$(package_of "$path")
	if [ -n "$package" ]; then
		case " $given " in
		*" $package "*) continue ;;
		esac
		whole "the change touches $path, in $package, which the packages it was given ($given) do not name"
	fi
	case "$path" in
	.beads/issues.jsonl | .beads/interactions.jsonl) continue ;;
	esac
	if [ "$kind" = race ]; then
		case "$path" in
		*.md)
			case "$path" in
			*/*) parent=${path%/*} ;;
			*) parent=. ;;
			esac
			holds_go "$parent" || continue
			;;
		esac
	fi
	whole "the change touches $path, outside every Go package, which the checks read"
done <<EOF
$changed
EOF

if [ -z "$given" ]; then
	say "the change touches no Go package, and none of the $count files it touches since its merge base with $target is one this check reads, so there is no package to run it over"
	echo ""
	exit 0
fi
say "runs over $given, the packages holding the $count files this change touches since its merge base with $target"
echo "$given"
