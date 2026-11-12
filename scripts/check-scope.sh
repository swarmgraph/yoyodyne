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
# an empty line for none -- and why on standard error, and exits 0. For `test`,
# a second line reading "documents" says the change also touches files outside
# every package, and the Makefile then runs the tests that read this checkout
# (DOCUMENT_TESTS) beside the packages. It exits non-zero only when it could not
# decide, and the Makefile then runs the whole module: every doubt costs time
# rather than a check.
#
# The harness's list names the packages that hold a changed file, and nothing
# about the files outside every package, which the checks here read too: the
# tests read the documents, the Makefile, the scripts, and the configuration.
# The tests that climb out of their package to read them are the ones the
# Makefile's DOCUMENT_TESTS names, and internal/composition holds that list
# against the source. So:
#
#   - "./..." runs the whole module. For the race check, the whole module is
#     first put to scripts/race-skip.sh, which skips it for a change that is
#     only Markdown and tracker exports outside every Go package's directory,
#     exactly as `make race` with nothing named does.
#   - A list runs those packages, and only those. A package the change touches
#     that the list leaves out is added to them rather than widening the check
#     to the whole module. A file outside every package -- the Makefile, a
#     script, the configuration, a document -- does not widen it either: the
#     race check ignores it, and `test` runs the tests DOCUMENT_TESTS names
#     beside the packages. Neither check counts the tracker's exports. Only Git
#     being unable to say what changed runs the whole module.
#   - Nothing is a change that touches no Go package. Where the change touches
#     only Markdown outside every Go package's directory and tracker exports,
#     the race check runs over nothing and `test` runs only the tests that read
#     documents, each saying so. Where it touches any other file outside every
#     package, there are no packages to stand in for the check, so the whole
#     module runs.
#
# A list is kept narrow because the whole module is what a check given no list
# runs, and several narrowed checks each running the whole module side by side
# load the disk until nothing else on the machine finishes in time. Narrowing
# here is safe because it is not the last word: the harness checks the approved
# merge candidate once more with the whole module before it is promoted
# (docs/configuration.md, "Where the whole suite runs"), so a package that
# depends on a changed one, or a test that reads a file outside every package
# and is missing from DOCUMENT_TESTS, fails there rather than on the target
# branch.
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
documents=0
outside=
extra=
while IFS= read -r path; do
	[ -n "$path" ] || continue
	count=$((count + 1))
	package=$(package_of "$path")
	if [ -n "$package" ]; then
		case " $given $extra " in
		*" $package "*) continue ;;
		esac
		# A package the list leaves out joins it rather than widening the check:
		# it is one more package to test, not a reason to test every package.
		extra="$extra $package"
		continue
	fi
	case "$path" in
	.beads/issues.jsonl | .beads/interactions.jsonl) continue ;;
	esac
	case "$path" in
	*.md)
		case "$path" in
		*/*) parent=${path%/*} ;;
		*) parent=. ;;
		esac
		if ! holds_go "$parent"; then
			documents=$((documents + 1))
			continue
		fi
		;;
	esac
	# Any other file outside every package -- the Makefile, a script, the
	# configuration -- widens only a check that was given no package. Given a
	# list, the listed packages run, `test` adds the tests that read this
	# checkout, and the whole module is left to the merge candidate's check.
	[ -n "$given" ] || whole "the change touches $path, outside every Go package, which the checks read, and the change names no Go package to run them over"
	documents=$((documents + 1))
	[ -n "$outside" ] || outside=$path
done <<EOF
$changed
EOF

packages=$(echo $given $extra)
because="the packages holding the $count files this change touches since its merge base with $target"
if [ -n "$extra" ]; then
	because="$because, with$extra, which the packages it was given (${given:-none}) do not name"
fi
if [ -n "$outside" ]; then
	because="$because; it also touches $outside outside every Go package, which the whole module's check of the merge candidate judges"
fi

# The race check reads no document; `test` runs the tests that read them.
if [ "$kind" = race ] || [ "$documents" -eq 0 ]; then
	if [ -z "$packages" ]; then
		say "the change touches no Go package, and none of the $count files it touches since its merge base with $target is one this check reads, so there is no package to run it over"
		echo ""
		exit 0
	fi
	say "runs over $packages, $because"
	echo "$packages"
	exit 0
fi
if [ -z "$packages" ]; then
	say "the change touches no Go package, and all $count files it touches since its merge base with $target are documents outside every package or tracker exports, so only the tests that read documents run"
else
	say "runs over $packages, $because, and the tests that read this checkout, for the $documents files it touches outside every package"
fi
printf '%s\ndocuments\n' "$packages"
