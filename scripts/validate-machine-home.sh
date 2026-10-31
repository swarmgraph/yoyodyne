#!/usr/bin/env bash
#
# validate-machine-home.sh - the validation project. It proves, on a machine
# that has never run Yoyodyne, that a contributor's adoption works end to end:
# fresh Git repositories holding none of the harness's files, a home directory
# of their own, and the seven checks of the machine home's validation
# (docs/designs/machine-home.md#validation) asserted one by one, each printed
# with its number and name as it passes or fails.
#
#   scripts/validate-machine-home.sh        what `make validate-machine-home` runs
#   VALIDATION_RECORD=<file> scripts/validate-machine-home.sh
#                                           also keep the run's measurements in <file>
#
# Every check runs under a home directory of its own inside one temporary root,
# with HOME pointed there and YOYODYNE_STATE_HOME and XDG_STATE_HOME unset except
# where a check sets them, so the real ~/.yoyodyne, the platform's earlier home,
# and ~/.config are never read or written. The root is removed on exit, however
# the script ends.
#
# It exits non-zero when any of checks 1 to 6 fails and zero otherwise. Check 7,
# the companion intent repository, is reported as not yet built and counted
# neither way until the contributor-mode work (yoyodyne-ifd.78) builds it. A
# behaviour a check describes that the harness lacks is a failure of that
# check, never a check made easier.
#
# Requires go and git. A run in flight is a record written through the
# harness's own run store by scripts/validationfixture, whose lease a
# background process holds for at most two minutes, so nothing it starts
# outlives the script.

set -uo pipefail

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

for tool in go git; do
  command -v "$tool" >/dev/null 2>&1 || { echo "validate-machine-home.sh needs $tool" >&2; exit 2; }
done

case "$(uname -s)" in
  (Darwin) earlier_within="Library/Application Support/Yoyodyne/state" ;;
  (Linux) earlier_within=".local/state/yoyodyne" ;;
  (*) echo "validate-machine-home.sh knows where earlier builds kept the state on macOS and Linux, not on $(uname -s)" >&2; exit 2 ;;
esac

scratch="$(mktemp -d "${TMPDIR:-/tmp}/yoyodyne-validate.XXXXXX")"
scratch="$(cd "$scratch" && pwd)"
cleanup() {
  # A lease holder still waiting is told to stop; it stops by itself within
  # its bound in any case.
  touch "$scratch"/stop-* 2>/dev/null || true
  wait 2>/dev/null || true
  chmod -R u+rwx "$scratch" 2>/dev/null || true
  rm -rf "$scratch"
}
trap cleanup EXIT

# The repositories are made under this root, so it has to be outside every Git
# repository, for the reason walk-adoption.sh gives: `bd`, which `yoyo init`
# and a start ask, discovers an enclosing repository's tracker.
if enclosing="$(git -C "$scratch" rev-parse --absolute-git-dir 2>/dev/null)"; then
  {
    printf 'validate-machine-home.sh needs a scratch root outside every git repository.\n'
    printf '  scratch root:         %s\n' "$scratch"
    printf '  enclosing repository: %s\n' "$enclosing"
    printf 'Point TMPDIR somewhere outside any repository and run it again:\n'
    printf '  TMPDIR=/tmp %s\n' "${BASH_SOURCE[0]}"
  } >&2
  exit 2
fi

# The builds and the Go caches are settled before HOME moves, because Go finds
# its caches from HOME.
GOCACHE="${GOCACHE:-$(go env GOCACHE)}"
GOMODCACHE="${GOMODCACHE:-$(go env GOMODCACHE)}"
export GOCACHE GOMODCACHE
mkdir -p "$scratch/bin"
if ! (cd "$repository" && go build -o "$scratch/bin/yoyo" ./cmd/yoyo && go build -o "$scratch/bin/validationfixture" ./scripts/validationfixture); then
  echo "validate-machine-home.sh could not build yoyo from $repository" >&2
  exit 2
fi
yoyo="$scratch/bin/yoyo"
fixture="$scratch/bin/validationfixture"

export GIT_AUTHOR_NAME="Validation Project" GIT_AUTHOR_EMAIL="validation@example.invalid"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME" GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
export GIT_CONFIG_NOSYSTEM=1

failed_checks=0
check_failures=0
current=""
step()  { printf '  - %s\n' "$*"; }
pass()  { printf '      ok: %s\n' "$*"; }
fail()  { printf '      FAIL: %s\n' "$*"; check_failures=$((check_failures + 1)); }
begin() { current="$1"; check_failures=0; printf '\n=== check %s\n' "$current"; }
finish() {
  if [ "$check_failures" -eq 0 ]; then
    printf 'check %s: passed\n' "$current"
  else
    printf 'check %s: FAILED (%d assertion(s))\n' "$current" "$check_failures"
    failed_checks=$((failed_checks + 1))
  fi
}
oneline() { tr '\n' ' ' | cut -c1-600; }
contains() { case "$1" in (*"$2"*) pass "$3" ;; (*) fail "$3 -- got: $(printf '%s' "$1" | oneline)" ;; esac; }
lacks()    { case "$1" in (*"$2"*) fail "$3 -- got: $(printf '%s' "$1" | oneline)" ;; (*) pass "$3" ;; esac; }
# real names a path the way the operating system resolves it, so /tmp and
# /private/tmp on macOS compare equal. A path that no longer exists has its
# /private prefix taken off instead.
real() { if [ -e "$1" ]; then (cd "$1" 2>/dev/null && pwd -P) || printf '%s' "$1"; else printf '%s' "${1#/private}"; fi; }
same_path() { [ "$(real "$1")" = "$(real "$2")" ] || [ "${1#/private}" = "${2#/private}" ]; }
json_field() { sed -n "s/^ *\"$2\": \"\(.*\)\",\{0,1\}\$/\1/p" "$1" | head -1; }
listing() { (cd "$1" 2>/dev/null && find . | LC_ALL=C sort) || true; }
count() { grep -c -- "$2" "$1" 2>/dev/null || true; }

# fresh_home gives a check a home directory of its own, with nothing of the
# harness's in it.
fresh_home() {
  export HOME="$scratch/$1/home"
  mkdir -p "$HOME"
  unset YOYODYNE_STATE_HOME XDG_STATE_HOME
  work="$scratch/$1"
}

# new_repository makes a Git repository with one empty commit and, where a
# remote is given, an origin. It holds none of the harness's files.
new_repository() {
  git init -q -b main "$1" && git -C "$1" commit -q --allow-empty -m "an empty repository" || return 1
  if [ -n "${2:-}" ]; then git -C "$1" remote add origin "$2"; fi
}

# adopt runs `yoyo init` in a repository and commits what it wrote, as a
# contributor adopting the harness does.
adopt() {
  (cd "$1" && "$yoyo" init --product "$2" </dev/null >"$work/init-$2.out" 2>&1) || return 1
  git -C "$1" add -A && git -C "$1" commit -q -m "adopt yoyodyne"
}

# start is a start of the harness: `yoyo status` opens the product's state the
# way every part of the product does, and binds a project on its first start.
start() {
  (cd "$1" && "$yoyo" status </dev/null >"$work/start.out" 2>"$work/start.err")
}

# What a run records about first use. Each is "not taken" with its reason when
# it was not measured, never a zero.
measure_empty_at=""
setup_failures=()
record_setup_failures() {
  local file="$1" stage="$2" line
  while IFS= read -r line; do
    local next
    next="$(printf '%s' "$line" | sed -n 's/.*`\([^`]*\)`.*/\1/p' | head -1)"
    if [ -n "$next" ]; then
      next="the documented next step, \`$next\`, was not tried: this run sets up neither a tracker remote nor a provider, so whether it fixes this is not measured"
    else
      next="the message names no next step, so none was tried"
    fi
    setup_failures+=("at $stage: $(printf '%s' "$line" | cut -c1-400) -- $next")
  done < <(grep -i -E 'failed|error|refus' "$file" 2>/dev/null || true)
}

printf 'The validation project: the machine home, proved against fresh repositories\n'
printf 'scratch root: %s (removed on exit)\n' "$scratch"
printf 'yoyo: %s\n' "$("$yoyo" version 2>&1 | head -1)"

# ---------------------------------------------------------------------------
begin "1 (home layout)"
fresh_home check1
measure_empty_at="$(date '+%Y-%m-%d %H:%M:%S %Z')"
step "a fresh repository adopted with \`yoyo init\`, then a first start"
new_repository "$work/demo" "https://example.invalid/acme/demo.git" || fail "could not make the fresh repository"
if adopt "$work/demo" demo; then pass "\`yoyo init\` wrote the project's configuration"; else fail "\`yoyo init\` failed: $(oneline <"$work/init-demo.out")"; fi
record_setup_failures "$work/init-demo.out" "\`yoyo init\`"
start "$work/demo"; code=$?
record_setup_failures "$work/start.err" "the first start"
cp -f "$work/start.err" "$work/first-start.err"
if [ "$code" -eq 0 ]; then pass "the first start ran"; else fail "the first start exited $code: $(oneline <"$work/start.err")"; fi
if [ -d "$HOME/.yoyodyne" ]; then
  pass "the harness's state is under ~/.yoyodyne"
  extra=""
  # machine.yaml is the state-root setting's own file, which the v1 harness
  # design keeps in the home whatever it says; the rest is the design's list.
  for entry in $(ls -A "$HOME/.yoyodyne"); do
    case "$entry" in (projects|accounts|operator-hold.json|logs|machine.yaml) ;; (*) extra="$extra $entry" ;; esac
  done
  if [ -z "$extra" ]; then pass "the top of the home holds only projects, accounts, operator-hold.json and logs"
  else fail "the top of the home holds something no single project owns beyond projects, accounts, operator-hold.json and logs:$extra"; fi
else
  fail "nothing was written under ~/.yoyodyne"
fi
# Other tools keep their own directories under ~/.config (the tracker writes
# one), so it is held to holding nothing of Yoyodyne's rather than nothing.
ours="$(ls -A "$HOME/.config" 2>/dev/null | grep -i yoyo || true)"
if [ -z "$ours" ]; then pass "nothing of the harness's under ~/.config"; else fail "the harness wrote under ~/.config: $ours"; fi
if [ -e "$HOME/$earlier_within" ]; then fail "the harness wrote outside the home, at $HOME/$earlier_within"; else pass "nothing under $HOME/$earlier_within"; fi

step "the settings that name a home, in the order the v1 harness design rules"
fresh_home check1-precedence
mkdir -p "$HOME/.yoyodyne"
printf 'state_root: %s\n' "$work/from-setting" >"$HOME/.yoyodyne/machine.yaml"
resolved="$(cd "$work" && YOYODYNE_STATE_HOME="$work/from-variable" XDG_STATE_HOME="$work/from-xdg" "$yoyo" project list --home 2>&1)"
if same_path "$resolved" "$work/from-variable"; then pass "YOYODYNE_STATE_HOME is taken over the state-root setting and XDG_STATE_HOME"
else fail "with all three set the home was $resolved, not YOYODYNE_STATE_HOME's $work/from-variable"; fi
resolved="$(cd "$work" && XDG_STATE_HOME="$work/from-xdg" "$yoyo" project list --home 2>&1)"
if same_path "$resolved" "$work/from-setting"; then pass "the state-root setting is taken over XDG_STATE_HOME"
else fail "with the setting and XDG_STATE_HOME set the home was $resolved, not the setting's $work/from-setting"; fi
rm -f "$HOME/.yoyodyne/machine.yaml"
resolved="$(cd "$work" && XDG_STATE_HOME="$work/from-xdg" "$yoyo" project list --home 2>&1)"
if same_path "$resolved" "$work/from-xdg/yoyodyne"; then pass "XDG_STATE_HOME is taken over the default"
else fail "with XDG_STATE_HOME set the home was $resolved, not $work/from-xdg/yoyodyne"; fi
resolved="$(cd "$work" && "$yoyo" project list --home 2>&1)"
if same_path "$resolved" "$HOME/.yoyodyne"; then pass "with nothing set the home is ~/.yoyodyne"
else fail "with nothing set the home was $resolved, not $HOME/.yoyodyne"; fi
new_repository "$work/other" "" && adopt "$work/other" other || fail "could not adopt a second fresh repository"
(cd "$work/other" && YOYODYNE_STATE_HOME="$work/named" "$yoyo" status </dev/null >/dev/null 2>"$work/named.err"); code=$?
if [ "$code" -eq 0 ] && [ -f "$work/named/projects/other/repository.json" ] && [ ! -e "$HOME/.yoyodyne/projects/other" ]; then
  pass "a start with YOYODYNE_STATE_HOME set keeps its state under the directory it names"
else fail "a start with YOYODYNE_STATE_HOME=$work/named exited $code, and its state is not only there: $(oneline <"$work/named.err")"; fi

step "an unsafe product.id"
fresh_home check1-unsafe
new_repository "$work/unsafe" "" && adopt "$work/unsafe" unsafe || fail "could not adopt the repository"
sed -e 's/^  id: unsafe$/  id: Not\/Safe/' "$work/unsafe/.yoyodyne/config.yaml" >"$work/config.yaml" && cp -f "$work/config.yaml" "$work/unsafe/.yoyodyne/config.yaml"
start "$work/unsafe"; code=$?
said="$(cat "$work/start.err")"
if [ "$code" -ne 0 ]; then pass "the start is refused"; else fail "a configuration with product.id Not/Safe started"; fi
contains "$said" "Not/Safe" "the refusal names the id"
if [ -z "$(ls -A "$HOME/.yoyodyne/projects" 2>/dev/null)" ]; then pass "no project directory was made"
else fail "a project directory was made: $(ls -A "$HOME/.yoyodyne/projects")"; fi
finish

# ---------------------------------------------------------------------------
begin "2 (first start)"
fresh_home check1
binding="$HOME/.yoyodyne/projects/demo/repository.json"
contains "$(cat "$work/first-start.err")" "bound it to the repository at" "the first start says it bound the project"
if [ -f "$binding" ]; then
  pass "repository.json was written"
  common="$(git -C "$work/demo" rev-parse --path-format=absolute --git-common-dir)"
  recorded="$(json_field "$binding" git_common_directory)"
  if same_path "$recorded" "$common"; then pass "it records the Git common directory, $recorded"
  else fail "it records the Git common directory as '$recorded', not $common"; fi
  case "$recorded" in (/*) pass "the common directory is absolute" ;; (*) fail "the common directory '$recorded' is not absolute" ;; esac
  remote="$(json_field "$binding" remote_url)"
  if [ "$remote" = "https://example.invalid/acme/demo.git" ]; then pass "it records the remote URL"; else fail "it records the remote URL as '$remote'"; fi
  at="$(json_field "$binding" bound_at)"
  case "$at" in ([0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]T*) pass "it records when, $at" ;; (*) fail "it records the time as '$at'" ;; esac
  by="$(json_field "$binding" bound_by)"
  if [ -n "$by" ]; then pass "it records who, $by"; else fail "it records nobody as having bound it"; fi
else
  fail "no repository.json at $binding"
fi
step "a start from a Git worktree of the same repository"
cp -f "$binding" "$work/binding-before" 2>/dev/null
git -C "$work/demo" worktree add -q "$work/demo-worktree" -b validation-side 2>/dev/null || fail "could not add a worktree"
start "$work/demo-worktree"; code=$?
if [ "$code" -eq 0 ]; then pass "the worktree starts"; else fail "the worktree's start exited $code: $(oneline <"$work/start.err")"; fi
lacks "$(cat "$work/start.err")" "created the project directory" "it does not bind a second time"
projects="$(ls -A "$HOME/.yoyodyne/projects" | tr '\n' ' ')"
if [ "$projects" = "demo " ]; then pass "it is the same project, with no other project directory"; else fail "the projects are now: $projects"; fi
if cmp -s "$binding" "$work/binding-before"; then pass "the binding is unchanged"; else fail "the worktree's start changed the binding"; fi
finish

# ---------------------------------------------------------------------------
begin "3 (second clone)"
fresh_home check1
step "a second clone of the same project"
git clone -q "$work/demo" "$work/demo-clone" && git -C "$work/demo-clone" remote set-url origin "https://example.invalid/acme/demo.git" || fail "could not clone"
before="$(listing "$HOME/.yoyodyne")"; cp -f "$binding" "$work/binding-before"
start "$work/demo-clone"; code=$?
said="$(cat "$work/start.err")"
if [ "$code" -ne 0 ]; then pass "the start is refused"; else fail "a second clone started"; fi
contains "$said" "$work/demo," "it names the bound clone's path"
contains "$said" "$work/demo-clone" "it names the second clone's path"
contains "$said" "\`yoyo project bind --replace\`" "it names the remedy command"
commands="$(printf '%s' "$said" | grep -o '`yoyo [^`]*`' | wc -l | tr -d ' ')"
if [ "$commands" = "1" ]; then pass "it names one command"; else fail "it names $commands commands"; fi
if [ "$(listing "$HOME/.yoyodyne")" = "$before" ] && cmp -s "$binding" "$work/binding-before"; then pass "nothing was written to the home"
else fail "the refused start wrote to the home"; fi

step "a second product sharing the id"
new_repository "$work/stranger" "https://example.invalid/acme/stranger.git" && adopt "$work/stranger" demo || fail "could not adopt the second product"
before="$(listing "$HOME/.yoyodyne")"
start "$work/stranger"; code=$?
said="$(cat "$work/start.err")"
if [ "$code" -ne 0 ]; then pass "the start is refused"; else fail "a second product with the id demo started"; fi
contains "$said" "$work/demo " "it names the bound repository's path"
contains "$said" "$work/stranger " "it names the second repository's path"
contains "$said" "\`yoyo project rename demo <new>\`" "it names the rename command"
commands="$(printf '%s' "$said" | grep -o '`yoyo [^`]*`' | wc -l | tr -d ' ')"
if [ "$commands" = "1" ]; then pass "it names one command"; else fail "it names $commands commands"; fi
if [ "$(listing "$HOME/.yoyodyne")" = "$before" ] && cmp -s "$binding" "$work/binding-before"; then pass "nothing was written to the home"
else fail "the refused start wrote to the home"; fi

step "the rename remedy, then the second product starts"
sed -e 's/^  id: demo$/  id: demo-first/' "$work/demo/.yoyodyne/config.yaml" >"$work/config.yaml" && cp -f "$work/config.yaml" "$work/demo/.yoyodyne/config.yaml"
git -C "$work/demo" commit -q -am "rename the product" || fail "could not commit the new id"
(cd "$work/demo" && "$yoyo" project rename demo demo-first </dev/null >"$work/rename.out" 2>&1); code=$?
if [ "$code" -eq 0 ]; then pass "\`yoyo project rename demo demo-first\` ran"; else fail "the rename exited $code: $(oneline <"$work/rename.out")"; fi
start "$work/stranger"; code=$?
if [ "$code" -eq 0 ]; then pass "the second product starts after the rename"; else fail "the second product's start exited $code: $(oneline <"$work/start.err")"; fi
contains "$(cat "$work/start.err")" "bound it to the repository at $work/stranger" "it binds the id to itself and says so"
start "$work/demo"; code=$?
if [ "$code" -eq 0 ]; then pass "the renamed project still starts"; else fail "the renamed project's start exited $code: $(oneline <"$work/start.err")"; fi
finish

# ---------------------------------------------------------------------------
begin "4 (moved clone)"
fresh_home check4
new_repository "$work/moving" "https://example.invalid/acme/moving.git" && adopt "$work/moving" moving || fail "could not adopt the repository"
start "$work/moving" || fail "the first start failed: $(oneline <"$work/start.err")"
binding="$HOME/.yoyodyne/projects/moving/repository.json"
mv -f "$work/moving" "$work/moved"
cp -f "$binding" "$work/binding-before"
start "$work/moved"; code=$?
said="$(cat "$work/start.err")"
if [ "$code" -ne 0 ]; then pass "a start from the moved clone is refused"; else fail "the moved clone started"; fi
contains "$said" "\`yoyo project bind\`" "it names yoyo project bind"
contains "$said" "$work/moving," "it names the path the binding records"
if cmp -s "$binding" "$work/binding-before"; then pass "the binding is unchanged"; else fail "the refused start changed the binding"; fi
step "yoyo project bind with no run in flight"
(cd "$work/moved" && "$yoyo" project bind </dev/null >"$work/bind.out" 2>&1); code=$?
if [ "$code" -eq 0 ]; then pass "bind rebinds"; else fail "bind exited $code: $(oneline <"$work/bind.out")"; fi
recorded="$(json_field "$binding" git_common_directory)"
if same_path "$recorded" "$work/moved/.git"; then pass "the binding names the moved clone"; else fail "the binding names '$recorded'"; fi
by="$(json_field "$binding" bound_by)"
contains "$by" "yoyo project bind" "the binding records who rebound it"
acts="$HOME/.yoyodyne/projects/moving/state/project-acts.jsonl"
contains "$(cat "$acts" 2>/dev/null)" "\"act\":\"bind\"" "the project's state records the bind as an act"
start "$work/moved"; code=$?
if [ "$code" -eq 0 ]; then pass "the moved clone starts"; else fail "the moved clone's start exited $code: $(oneline <"$work/start.err")"; fi
step "yoyo project bind with a run in flight, after the clone moves again"
# The run is never ended: a run moves between states only through the harness's
# own steps, so the refusal is asked of a second move rather than before the
# bind above.
mv -f "$work/moved" "$work/moved-again"
cp -f "$binding" "$work/binding-before"
run_id="$("$fixture" run "$HOME/.yoyodyne" moving)" || fail "could not record a run in flight"
(cd "$work/moved-again" && "$yoyo" project bind </dev/null >"$work/bind.out" 2>&1); code=$?
said="$(cat "$work/bind.out")"
if [ "$code" -ne 0 ]; then pass "bind is refused"; else fail "bind went ahead with a run in flight"; fi
contains "$said" "in flight" "it says a run is in flight"
contains "$said" "$run_id" "it names the run"
if cmp -s "$binding" "$work/binding-before"; then pass "the binding is unchanged"; else fail "the refused bind changed the binding"; fi
finish

# ---------------------------------------------------------------------------
# earlier_home lays out a home the way builds before the machine home kept it,
# under a fresh home directory: one product's records under products/, a
# preserved worktree under worktrees/, a run record naming that worktree, and
# the repository's state-root marker naming that home, as the earlier build's
# own start left it.
earlier_home() {
  fresh_home "$1"
  earlier="$HOME/$earlier_within"
  mkdir -p "$earlier/products/legacy" "$earlier/worktrees/legacy/legacy"
  new_repository "$work/legacy" "https://example.invalid/acme/legacy.git" && adopt "$work/legacy" legacy || return 1
  start "$work/legacy" || return 1
  git -C "$work/legacy" worktree add -q "$earlier/worktrees/legacy/legacy/run-kept" -b run-kept || return 1
  legacy_run="$("$fixture" ended "$earlier" legacy "$earlier/worktrees/legacy/legacy/run-kept" run-kept "$(git -C "$work/legacy" rev-parse HEAD)")"
}

# the commands check 5 holds to the warning: the ones that open the product's
# state, and the ones that read the home or the documents without opening it.
warning_commands=("status" "reports" "cost" "directive list" "config validate" "project list" "goals list" "artifact list" "stale")

begin "5 (old home only)"
earlier_home check5 || fail "could not lay out the earlier home"
before="$(listing "$earlier/products")"
for command in "${warning_commands[@]}"; do
  # shellcheck disable=SC2086 # each entry is a command and its subcommand
  (cd "$work/legacy" && "$yoyo" $command </dev/null >/dev/null 2>"$work/warn.err")
  warned="$(count "$work/warn.err" "yoyo home migrate")"
  if [ "$warned" = "1" ]; then pass "\`yoyo $command\` warns once on standard error, naming yoyo home migrate"
  else fail "\`yoyo $command\` warned $warned time(s) on standard error naming yoyo home migrate"; fi
done
(cd "$work/legacy" && "$yoyo" doctor </dev/null >"$work/doctor.out" 2>&1)
contains "$(cat "$work/doctor.out")" "yoyo home migrate" "yoyo doctor reports the old home and names yoyo home migrate"
if [ ! -e "$HOME/.yoyodyne/projects" ]; then pass "nothing was moved into ~/.yoyodyne"; else fail "the commands made $HOME/.yoyodyne/projects"; fi
if [ "$(listing "$earlier/products")" = "$before" ] || [ -d "$earlier/products/legacy" ]; then pass "the product's records are still in the old home"
else fail "the product's records left the old home"; fi
if [ -d "$earlier/worktrees/legacy/legacy/run-kept" ]; then pass "the preserved worktree is where it was"; else fail "the preserved worktree moved"; fi
finish

# ---------------------------------------------------------------------------
begin "6 (migration)"
earlier_home check6-twin || fail "could not lay out the twin earlier home"
twin_home="$HOME" twin_earlier="$earlier" twin_work="$work" twin_legacy_run="$legacy_run"
# The twin is given the same run in flight, held and ended, so the two homes
# differ only in whether their migration was interrupted.
twin_run="$("$fixture" run "$earlier" legacy)" && touch "$scratch/stop-twin" &&
  "$fixture" hold "$earlier" legacy "$twin_run" "$scratch/stop-twin" 1 >/dev/null || fail "could not record the twin's run"
(cd "$work/legacy" && "$yoyo" home migrate </dev/null >"$work/migrate.out" 2>&1) || fail "the uninterrupted migration exited non-zero: $(oneline <"$work/migrate.out")"

earlier_home check6 || fail "could not lay out the earlier home"
step "yoyo home migrate with a run in flight"
held_run="$("$fixture" run "$earlier" legacy)" || fail "could not record a run in flight"
"$fixture" hold "$earlier" legacy "$held_run" "$scratch/stop-check6" 120 >"$work/hold.out" 2>&1 &
holder=$!
for _ in $(seq 1 100); do grep -q held "$work/hold.out" 2>/dev/null && break; sleep 0.1; done
(cd "$work/legacy" && "$yoyo" home migrate </dev/null >"$work/migrate.out" 2>&1); code=$?
said="$(cat "$work/migrate.out")"
if [ "$code" -ne 0 ]; then pass "the migration is refused"; else fail "the migration went ahead with a run in flight"; fi
contains "$said" "$held_run" "it names the run in flight"
if [ -d "$earlier/products/legacy" ] && [ ! -e "$HOME/.yoyodyne/projects" ]; then pass "nothing moved"; else fail "something moved while the run was in flight"; fi
# Once the lease is let go nothing holds the run, as when a run's process has
# ended, and the migration goes ahead.
touch "$scratch/stop-check6"; wait "$holder" 2>/dev/null

step "a migration interrupted part way, then run again"
chmod 0500 "$earlier/worktrees/legacy/legacy"
(cd "$work/legacy" && "$yoyo" home migrate </dev/null >"$work/migrate-interrupted.out" 2>&1)
chmod 0700 "$earlier/worktrees/legacy/legacy"
if [ -d "$earlier/worktrees/legacy/legacy/run-kept" ] && [ -d "$HOME/.yoyodyne/projects/legacy/state" ]; then
  pass "the first migration stopped part way: the records moved and the worktree did not"
else
  fail "the migration could not be interrupted here (the worktree moved, or the records did not): $(oneline <"$work/migrate-interrupted.out")"
fi
(cd "$work/legacy" && "$yoyo" home migrate </dev/null >"$work/migrate.out" 2>&1); code=$?
if [ "$code" -eq 0 ]; then pass "the migration run again finishes"; else fail "the migration run again exited $code: $(oneline <"$work/migrate.out")"; fi
# The two runs' records carry run ids of their own, so the layouts are
# compared with each id written the same way.
layout() { listing "$1" | sed -E 's/run-[0-9a-f]{32}/run-ID/g'; }
if [ "$(layout "$HOME/.yoyodyne")" = "$(layout "$twin_home/.yoyodyne")" ] && [ "$(layout "$earlier")" = "$(layout "$twin_earlier")" ]; then
  pass "it ends in the same layout as a migration never interrupted"
else
  { layout "$HOME/.yoyodyne"; layout "$earlier"; } >"$work/layout-interrupted"
  { layout "$twin_home/.yoyodyne"; layout "$twin_earlier"; } >"$work/layout-uninterrupted"
  fail "it ends in a different layout from a migration never interrupted (< interrupted, > not): $(diff "$work/layout-interrupted" "$work/layout-uninterrupted" | oneline)"
fi

# What a migration leaves is read off the twin, whose migration was never
# interrupted, so that what the interrupted one failed to finish is reported
# once, above, rather than again under every assertion below.
export HOME="$twin_home"; earlier="$twin_earlier" work="$twin_work" legacy_run="$twin_legacy_run"
moved_worktree="$HOME/.yoyodyne/projects/legacy/worktrees/run-kept"
step "after a migration: the moved worktree, its run record, and the old home"
if [ -d "$moved_worktree" ]; then
  listed="$(git -C "$moved_worktree" worktree list 2>&1)"
  contains "$listed" "$(real "$moved_worktree")" "git worktree list in the moved worktree shows its new path"
  lacks "$listed" "$earlier_within" "and no longer the old one"
  if git -C "$moved_worktree" status --short >/dev/null 2>&1; then pass "a Git command in the moved worktree succeeds"; else fail "git status fails in the moved worktree"; fi
else
  fail "no worktree at $moved_worktree"
fi
shown="$("$fixture" show "$HOME/.yoyodyne" legacy "$legacy_run" 2>&1)"
now_path="$(printf '%s\n' "$shown" | sed -n 1p)" kept_path="$(printf '%s\n' "$shown" | sed -n 2p)"
if same_path "$now_path" "$moved_worktree"; then pass "the run record names the new path"; else fail "the run record names '$now_path'"; fi
if [ -n "$kept_path" ] && same_path "$kept_path" "$earlier/worktrees/legacy/legacy/run-kept"; then pass "its migration note keeps the old path"
else fail "its migration note keeps '$kept_path', not the old path"; fi
marker="$earlier/moved-to-machine-home"
if [ -f "$marker" ]; then contains "$(cat "$marker")" "$HOME/.yoyodyne" "the old home holds a marker naming the new one"
else fail "the old home holds no marker"; fi

step "an older build, which resolves the old home, started after the migration"
# A build from before the machine home resolves the platform's earlier home as
# its default. This build is pointed at that home the one way it can be, and
# what refuses it is the same guard an older build consults: the state-root
# marker in the repository's Git directory, which has to name the new home.
before="$(listing "$earlier")"
(cd "$work/legacy" && YOYODYNE_STATE_HOME="$earlier" "$yoyo" status </dev/null >/dev/null 2>"$work/older.err"); code=$?
if [ "$code" -ne 0 ]; then pass "it is refused"; else fail "it started on the old home, and the repository's state-root marker names $(cat "$work/legacy/.git/yoyodyne/state-root" 2>/dev/null)"; fi
contains "$(cat "$work/older.err")" "$HOME/.yoyodyne" "the refusal names the new home"
if [ "$(listing "$earlier")" = "$before" ]; then pass "nothing was written to the old home"; else printf '%s\n' "$before" >"$work/older-before"; listing "$earlier" >"$work/older-after"
  fail "it wrote to the old home: $(diff "$work/older-before" "$work/older-after" | oneline)"; fi
finish

# ---------------------------------------------------------------------------
printf '\n=== check 7 (companion intent repository)\n'
printf 'check 7 (companion intent repository): not yet built, counted neither passed nor failed; it waits on the contributor-mode work (yoyodyne-ifd.78)\n'

# ---------------------------------------------------------------------------
{
  printf '\n=== first use, as this run measured it\n'
  printf 'empty repository made: %s\n' "$measure_empty_at"
  printf 'first independently reviewed change integrated: not taken -- reaching it needs a provider for the developer and the reviewer and a forge to integrate through, and this run reaches neither\n'
  printf 'time from the empty repository to that change: not taken, for the same reason\n'
  printf 'decisions asked of the person in the steps this run took: none; every command ran with no input, and none waited on one\n'
  printf 'brief approvals asked: not taken -- the brief is approved in the conversation with the Lead Product Manager, which needs a provider\n'
  printf 'goals approvals asked: not taken -- the goals are approved in that same conversation\n'
  if [ "${#setup_failures[@]}" -eq 0 ]; then
    printf 'setup failures: none\n'
  else
    printf 'setup failures (%d):\n' "${#setup_failures[@]}"
    for failure in "${setup_failures[@]}"; do printf '  - %s\n' "$failure"; done
  fi
} | tee "${VALIDATION_RECORD:-/dev/null}"

printf '\n'
if [ "$failed_checks" -eq 0 ]; then
  printf 'checks 1 to 6 passed; check 7 not yet built\n'
  exit 0
fi
printf '%d of checks 1 to 6 failed; check 7 not yet built\n' "$failed_checks"
exit 1
