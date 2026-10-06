#!/usr/bin/env bash
#
# walk-adoption.sh - execute the README's "Getting started" steps against a
# throwaway project that is not this one and is not written in Go, so that
# section is verified rather than asserted. This file's history is the reason: a
# README that asserted something false about the product once reached the
# operator as fact, and a claim a stranger will act on is worth executing before
# it ships.
#
# The README's spine is three steps -- install, `yoyo init`, `yoyo chat` -- and
# the numbered steps below are this script's own finer sequence through the
# claims those three make.
#
#   scripts/walk-adoption.sh                 walk every step that needs no provider
#   WALK_PROVIDER=1 scripts/walk-adoption.sh also invoke the provider on step 12
#
# The provider step is opt-in because it spends real capacity: it hands an item
# to a developer agent. Everything before it is free and deterministic.
#
# Requires go, git, and python3, and bd -- which, where the machine has none,
# is fetched from the tracker's one home at the version CI pins, into the
# scratch root, so a walk on a fresh machine and the walk CI runs install the
# tracker from the same place.
#
# No state an operator owns is written: the scratch project, the worktrees, the
# binary `go install` produces, and the run state all live under one temporary
# root that is removed on exit, so a real state directory is never touched. Two
# things outside it are written, and both are caches rather than state. Go's
# build cache goes to $GOCACHE, which defaults into the scratch root here and is
# left alone when a caller already set one; Go's module cache is deliberately
# shared, because isolating it would mean re-downloading the module graph on
# every walk to protect a directory whose whole purpose is to be rebuilt. The
# repository's own ./bin/yoyo is also rebuilt, which is what `make build` does
# and what the step is there to check.
#
# The documented claims this cannot check are the ones that need the network:
# whether the clone URL is reachable, and whether `go install` of a published tag
# actually fetches. It checks the verifiable halves instead -- that the URL the
# README names is this checkout's origin remote, and that the module path the
# README installs is the one go.mod declares -- and names the rest as skipped.

set -euo pipefail

readme_clone_url="https://github.com/swarmgraph/yoyodyne"
readme_install_module="github.com/mason-bryant/yoyodyne"
# The one home Beads has, which the README names and every other document here
# agrees with (TestEveryBeadsHomeThisRepositoryNamesIsTheCanonicalOne holds
# them to it): where a walk with no bd fetches the tracker from.
beads_home="https://github.com/gastownhall/beads"

repository="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# The default root is safe and needs nothing done to it: $TMPDIR is the user's
# own directory and /tmp is the system's, and neither is inside a git repository
# on an ordinary machine. A caller that has pointed TMPDIR at one is refused
# below, where the reason is written down.
scratch="$(mktemp -d "${TMPDIR:-/tmp}/yoyodyne-walk.XXXXXX")"
# TMPDIR often ends in a slash on macOS; normalize so path assertions compare
# against the same spelling the harness prints.
scratch="$(cd "$scratch" && pwd)"
project="$scratch/calc"
failures=0
skips=0

cleanup() {
  # Remove the scratch worktrees through git where the scratch repository still
  # exists, so nothing is left registered, then take the whole root.
  if [ -d "$project/.git" ]; then
    git -C "$project" worktree prune >/dev/null 2>&1 || true
  fi
  rm -rf "$scratch"
}
trap cleanup EXIT

# The scratch root has to be outside every git repository. The throwaway project
# is created inside it, and `bd init` there discovers the enclosing repository's
# tracker remote and tries to clone it -- which under a sandbox that denies the
# network dies at step 2 under `set -e` with nothing from bd in the log, so a
# misplaced temporary directory reads as a broken adoption path. That is not
# hypothetical: it cost run-626e851a a whole walkthrough, its TMPDIR having
# pointed into a harness run scratch directory under this repository's
# .git/worktrees. Refusing here rather than passing --no-remote to `bd init` is
# what the tracker actually offers -- `bd init` has --remote and no way to
# decline one -- and it is the answer that covers whatever else in the walk
# would find the enclosing repository next.
if enclosing="$(git -C "$scratch" rev-parse --absolute-git-dir 2>/dev/null)"; then
  {
    printf 'walk-adoption.sh needs a scratch root outside every git repository.\n'
    printf '  scratch root:         %s\n' "$scratch"
    printf '  enclosing repository: %s\n' "$enclosing"
    printf 'The throwaway project is created under that root, and `bd init` in it\n'
    printf "discovers the enclosing repository's tracker remote and tries to clone it.\n"
    printf 'Point TMPDIR somewhere outside any repository and walk again:\n'
    printf '  TMPDIR=/tmp %s\n' "${BASH_SOURCE[0]}"
  } >&2
  exit 2
fi

export YOYODYNE_STATE_HOME="$scratch/state"
export GOCACHE="${GOCACHE:-$scratch/gocache}"
mkdir -p "$YOYODYNE_STATE_HOME"

step()   { printf '\n=== %s\n' "$*"; }
run()    { printf '$ %s\n' "$*"; "$@"; }
pass()   { printf '  ok: %s\n' "$*"; }
fail()   { printf '  FAIL: %s\n' "$*"; failures=$((failures + 1)); }
# A claim this environment cannot exercise is named rather than passed over. A
# silent skip reads as coverage, which is the failure mode this whole script
# exists to avoid.
skip()   { printf '  SKIPPED: %s\n' "$*"; skips=$((skips + 1)); }

# contains asserts that a haystack holds a substring, which is how a claim about
# what a command *says* is checked: the README quotes these messages.
contains() {
  case "$1" in (*"$2"*) pass "$3" ;; (*) fail "$3 -- got: $1" ;; esac
}
missing() {
  case "$1" in (*"$2"*) fail "$3 -- got: $1" ;; (*) pass "$3" ;; esac
}

for tool in go git python3; do
  command -v "$tool" >/dev/null 2>&1 || { echo "walk-adoption.sh needs $tool" >&2; exit 2; }
done

# The tracker is the one prerequisite the README cannot have `go install`
# fetch, so a machine with none gets it the way the README's home for it says
# to: a prebuilt release from $beads_home, at the version CI pins -- read from
# the workflow, which is the one place that version is written -- into the
# scratch root, which is removed on exit. Nothing an operator owns is touched.
# A machine that already has bd walks with the one it has, as CI does after its
# own pinned install.
if ! command -v bd >/dev/null 2>&1; then
  bd_version="$(sed -n 's/^ *BD_VERSION: *\([0-9][0-9.]*\) *$/\1/p' "$repository/.github/workflows/ci.yml" | head -1)"
  if [ -z "$bd_version" ]; then
    echo "walk-adoption.sh needs bd, and could not read the version CI pins (BD_VERSION in .github/workflows/ci.yml) to fetch one" >&2
    exit 2
  fi
  case "$(uname -s)" in (Darwin) bd_os=darwin ;; (Linux) bd_os=linux ;;
    (*) echo "walk-adoption.sh needs bd, and there is no prebuilt tracker to fetch for $(uname -s); install it from $beads_home" >&2; exit 2 ;; esac
  case "$(uname -m)" in (x86_64|amd64) bd_arch=amd64 ;; (arm64|aarch64) bd_arch=arm64 ;;
    (*) echo "walk-adoption.sh needs bd, and there is no prebuilt tracker to fetch for $(uname -m); install it from $beads_home" >&2; exit 2 ;; esac
  bd_release="$beads_home/releases/download/v${bd_version}/beads_${bd_version}_${bd_os}_${bd_arch}.tar.gz"
  printf 'bd is not on PATH; fetching the tracker CI pins, v%s, from %s\n' "$bd_version" "$bd_release"
  mkdir -p "$scratch/bin" "$scratch/bd-release"
  if ! curl -fsSL -o "$scratch/bd-release/beads.tar.gz" "$bd_release"; then
    echo "walk-adoption.sh needs bd, and the pinned tracker v${bd_version} did not download from ${bd_release}; install it from $beads_home, or check BD_VERSION in .github/workflows/ci.yml" >&2
    exit 2
  fi
  tar -xzf "$scratch/bd-release/beads.tar.gz" -C "$scratch/bd-release"
  bd_binary="$(find "$scratch/bd-release" -maxdepth 2 -name bd -type f | head -1)"
  if [ -z "$bd_binary" ]; then
    echo "walk-adoption.sh needs bd, and the archive for the pinned tracker v${bd_version} carries no bd binary where this expects one" >&2
    exit 2
  fi
  install -m 0755 "$bd_binary" "$scratch/bin/bd"
  export PATH="$scratch/bin:$PATH"
  printf 'bd fetched into %s: %s\n' "$scratch/bin" "$(bd version 2>&1 | head -1)"
fi

step "prerequisites: the build requirement the README states"
go_directive="$(sed -n 's/^go \([0-9.]*\)$/\1/p' "$repository/go.mod")"
printf 'go.mod declares go %s\n' "$go_directive"
case "$go_directive" in (1.25*) pass "README's \"Go 1.25 or newer\" matches go.mod" ;;
  (*) fail "README says Go 1.25 or newer, go.mod declares $go_directive" ;; esac

step "1. install the binary"
# The README's first line is now a script fetched by URL, so the claim to check
# is that the URL names something this checkout actually has and that what it
# names runs. Whether raw.githubusercontent.com serves it needs the network,
# which this script does not assume; scripts/install-test.sh executes what the
# script does once it is running.
bootstrap_url="$(sed -n 's|.*\(https://raw.githubusercontent.com/[^ )`]*install\.sh\).*|\1|p' "$repository/README.md" | head -1)"
if [ -z "$bootstrap_url" ]; then
  fail "the README names no install script URL"
else
  printf 'install script: %s\n' "$bootstrap_url"
  bootstrap_path="${bootstrap_url#https://raw.githubusercontent.com/swarmgraph/yoyodyne/main/}"
  if [ -x "$repository/$bootstrap_path" ]; then
    pass "the URL the README installs from names $bootstrap_path, which this checkout has (reachability not checked)"
  else
    fail "the README installs from $bootstrap_url, and $bootstrap_path is not an executable file here"
  fi
  bootstrap_help="$(bash "$repository/$bootstrap_path" --help 2>&1 || true)"
  contains "$bootstrap_help" "--from-source" "the install script runs and documents its own flags"
fi

origin="$(git -C "$repository" remote get-url origin 2>/dev/null || echo "(none)")"
printf 'origin: %s\n' "$origin"
case "$origin" in (*swarmgraph/yoyodyne*) pass "README's clone URL names this checkout's origin (reachability not checked)" ;;
  (*) fail "README names $readme_clone_url, origin is $origin" ;; esac

# The README leads its install section with `go install`, which needs the module
# to be named by its repository path. That is a property of go.mod rather than a
# policy, so it is checked rather than trusted.
module="$(sed -n 's/^module \(.*\)$/\1/p' "$repository/go.mod")"
printf 'module: %s\n' "$module"
if [ "$module" = "$readme_install_module" ]; then
  pass "go.mod names the module the README's \`go install\` line names"
else
  fail "README installs $readme_install_module, go.mod declares $module"
fi
# Whether the tag is actually fetchable needs the network and a published tag,
# neither of which this script assumes. What it can settle offline is that the
# path is well-formed as a module path at all, which is exactly what the old
# `module yoyodyne` was not.
install_output="$(GOBIN="$scratch/gobin" go install "$readme_install_module/cmd/yoyo@latest" 2>&1 || true)"
missing "$install_output" "malformed module path" "\`go install\` reaches the proxy rather than refusing the path"

# The README tells a reader where the binary went, because an install that
# worked and a binary nobody can find look the same from the shell. The GOBIN
# half of that claim is what the install above just exercised; the default it
# names is go's own configuration, so it is read back rather than asserted, and
# only where this environment has not overridden it.
if [ -z "${GOBIN:-}" ] && [ -z "$(go env GOBIN)" ] && [ -z "${GOPATH:-}" ]; then
  gopath="$(go env GOPATH)"
  printf 'go env GOPATH: %s\n' "$gopath"
  if [ "$gopath" = "$HOME/go" ]; then
    pass "the documented default destination, ~/go/bin, is where go would install"
  else
    fail "README says the default is ~/go/bin, go reports $gopath/bin"
  fi
else
  skip "the ~/go/bin default: this environment sets GOPATH or GOBIN"
fi

if [ -x "$scratch/gobin/yoyo" ]; then
  pass "go install $readme_install_module/cmd/yoyo@latest produced a binary in \$GOBIN"
  # The README's verification step, run the way it is documented: put the
  # install directory on PATH, then ask the binary its version. Both halves are
  # the claim -- that the PATH addition is what makes `yoyo` resolvable at all,
  # and that what it prints is the tag it was installed at rather than "dev",
  # which is also the only check there is on the module-version stamping.
  resolved="$(PATH="$scratch/gobin:$PATH" command -v yoyo || true)"
  if [ "$resolved" = "$scratch/gobin/yoyo" ]; then
    pass "the documented PATH addition is what puts yoyo on PATH"
  else
    fail "adding the install directory to PATH did not resolve yoyo -- got: ${resolved:-nothing}"
  fi
  installed_version="$(PATH="$scratch/gobin:$PATH" yoyo version 2>&1 || true)"
  printf 'installed version: %s\n' "$installed_version"
  case "$installed_version" in
    (v*) pass "yoyo version prints the tag the module was installed at" ;;
    (*)  fail "yoyo version printed no release tag -- got: $installed_version" ;;
  esac
else
  skip "go install of a published tag, and the yoyo version check on it: needs network access and a pushed tag, neither assumed here"
fi

run make -C "$repository" build >/dev/null
yoyo="$repository/bin/yoyo"
if [ -x "$yoyo" ]; then pass "make build wrote ./bin/yoyo"; else fail "make build did not write ./bin/yoyo"; fi
# The README says a from-source build names the commit it came from rather than
# only saying "dev", which is a claim about the Makefile's version stamp.
built_version="$("$yoyo" version)"
printf 'built version: %s\n' "$built_version"
if [ "$built_version" = "dev" ]; then
  fail "make build reported \"dev\" rather than a git description"
else
  pass "yoyo version names the build it came from"
fi
# scripts/install.sh ends by asking the binary it installed whether it has
# `setup` and `doctor`, matching `yoyo help` against the pattern below, and
# leaves out the line naming either one it does not find. Its own suite answers
# with a fake binary, so this is the one place the real help text is held to
# that pattern: a help layout the pattern stops matching would otherwise drop
# both recommendations from every install without anything failing.
help_text="$("$yoyo" help 2>&1 || true)"
for verb in setup doctor; do
  if printf '%s\n' "$help_text" | grep -qE "^[[:space:]]+$verb([[:space:]]|\$)"; then
    pass "yoyo help lists $verb the way install.sh looks for it"
  else
    fail "yoyo help does not list $verb as an indented line, so install.sh would stop recommending it"
  fi
done

step "the scratch project: not this repository, not Go"
mkdir -p "$project/tests"
cd "$project"
git init -q -b main .
git config user.email walk@example.invalid
git config user.name "Adoption Walk"
# The README's init step configures the tracker from this project's own Git
# remote, so the walk gives it one. It is a bare repository beside the scratch
# project rather than a URL on a forge: what is being checked is what init reads
# and what it configures, and nothing here should need the network to say.
git init -q --bare "$scratch/origin.git"
git remote add origin "$scratch/origin.git"
printf 'def add(a, b):\n    return a + b\n' > calc.py
: > tests/__init__.py
# Step 4 asserts that init names this file as where it read the project's tests,
# so the path is named once here and used there rather than spelled twice.
fixture_test_file="tests/test_calc.py"
cat > "$fixture_test_file" <<'PY'
import unittest

from calc import add


class TestCalc(unittest.TestCase):
    def test_add(self):
        self.assertEqual(add(2, 2), 4)
PY
git add -A
git commit -qm "a tiny Python project"
pass "created a Python project with one commit at $project"

step "2. initialize the tracker"
run bd init >/dev/null 2>&1
if [ -d "$project/.beads" ]; then pass "bd init created .beads"; else fail "bd init created no .beads"; fi
run bd ready >/dev/null
pass "bd ready answers in the scratch project"

step "3. write the configuration"
written="$("$yoyo" init 2>&1)"
printf '%s\n' "$written"
for persona in architect developer development-manager product-manager program-manager reviewer; do
  if [ -f "$project/.yoyodyne/personas/$persona.md" ]; then
    pass "wrote personas/$persona.md"
  else
    fail "did not write personas/$persona.md"
  fi
done
refusal="$("$yoyo" init 2>&1 || true)"
contains "$refusal" "pass --force to overwrite it" "a second init refuses rather than overwriting"

# The README claims init leaves the tracker syncing over this project's own Git
# remote. That is checked where it matters -- in what bd holds afterwards --
# rather than only in what init said about it. Either outcome satisfies the
# claim: recent bd versions configure the remote themselves at `bd init` when
# the project already has one, and init reports leaving that alone rather than
# overwriting it.
contains "$written" "syncs through origin" "init leaves the tracker syncing over the project's Git remote"
tracker_remote="$(bd dolt remote list 2>&1 || true)"
printf '%s\n' "$tracker_remote"
contains "$tracker_remote" "$scratch/origin.git" "bd holds the sync remote init configured"

step "4. init proposes checks from what this project already declares"
# This project keeps its tests in tests/ and names no runner anywhere, so there
# are two plausible commands and nothing here says which one is the gate. What
# init must not do is pick one: it leaves both commented under a marker and
# leaves the list empty.
if grep -q '^checks: \[\]' .yoyodyne/config.yaml; then
  pass "an undecidable toolchain leaves checks empty rather than guessing"
else
  fail "a checks list was written for a project that names no test runner"
fi
if grep -q '^# YOU MUST CHOOSE' .yoyodyne/config.yaml; then
  pass "the candidates carry an explicit you-must-choose marker"
else
  fail "the candidates carry no you-must-choose marker"
fi
for candidate in "#  - python3 -m pytest -q" "#  - python3 -m unittest discover -q -s tests -t ."; do
  if grep -qF "$candidate" .yoyodyne/config.yaml; then
    pass "offers a candidate, commented out: $candidate"
  else
    fail "offers no candidate: $candidate"
  fi
done
if grep -qF "$fixture_test_file" .yoyodyne/config.yaml; then
  pass "names $fixture_test_file as where the candidates were derived from"
else
  fail "does not name $fixture_test_file as where the candidates came from"
fi
for language in "# Go" "# TypeScript / Node" "# Python" "# Java (Maven)"; do
  if grep -qF "$language" .yoyodyne/config.yaml; then
    pass "carries a commented $language example"
  else
    fail "carries no commented $language example"
  fi
done

# A project that does say which runner it uses gets a list it can run instead.
# `init --json` reports what was detected and what was written, so this is
# asserted rather than eyeballed. Both of the projects below are their own
# scratch directories, so the documented walk above is left undisturbed.
decided="$scratch/decided"
mkdir -p "$decided"
printf '[tool.pytest.ini_options]\naddopts = "-q"\n' > "$decided/pyproject.toml"
report="$("$yoyo" init --directory "$decided" --product decided --json || true)"
printf '%s\n' "$report"
summary="$(printf '%s' "$report" | python3 -c '
import json, sys

payload = json.load(sys.stdin)
written = ";".join(payload["checks"])
sources = ";".join(entry["source"] for entry in payload["detected"]["checks"])
print("written: %s from: %s" % (written, sources))
' || true)"
contains "$summary" "python3 -m pytest -q" "a project that names pytest gets a runnable check written"
contains "$summary" "pyproject.toml" "the report names the file the check was derived from"
if grep -q '^  # from pyproject.toml' "$decided/.yoyodyne/config.yaml"; then
  pass "the written check carries its provenance as a comment"
else
  fail "the written check carries no provenance comment"
fi

# Detection reads; it does not run. A Makefile whose target would leave a trace
# is how that is checked from outside: the target is detected and its trace is
# never written. The go.mod beside it makes this the supersede case too -- the
# Makefile is the project's own entry point, so the Go commands are offered
# rather than added, and offered is not the same as asked about.
untrusted="$scratch/untrusted"
mkdir -p "$untrusted"
printf 'check:\n\ttouch %s/ran\n' "$untrusted" > "$untrusted/Makefile"
printf 'module untrusted\n\ngo 1.24\n' > "$untrusted/go.mod"
"$yoyo" init --directory "$untrusted" --product untrusted >/dev/null || true
untrusted_config="$untrusted/.yoyodyne/config.yaml"
if grep -q '^  - make check$' "$untrusted_config"; then
  pass "a Makefile's check target is proposed as the project's gate"
else
  fail "a Makefile's check target was not proposed"
fi
if [ -e "$untrusted/ran" ]; then
  fail "init executed a target from the project it was configuring"
else
  pass "init read the Makefile without running anything in it"
fi
if grep -q '^# ALSO FOUND, AND NOT NEEDED' "$untrusted_config"; then
  pass "the superseded Go commands are headed as offered, not as owed"
else
  fail "the superseded Go commands carry no not-needed heading"
fi
if grep -qF '#  - go test ./...' "$untrusted_config"; then
  pass "the superseded Go commands are still shown, commented out"
else
  fail "the superseded Go commands were dropped rather than offered"
fi
# The demand to choose belongs only where a run cannot happen until somebody
# does. This configuration already runs, so it must not carry one.
if grep -q '^# YOU MUST CHOOSE' "$untrusted_config"; then
  fail "a configuration with a written checks list still demands a choice"
else
  pass "a configuration that already runs demands nothing"
fi

step "5. an empty checks list validates, but a run refuses it"
validate="$("$yoyo" config validate 2>&1)"
contains "$validate" "configuration valid" "config validate passes with checks: []"

# Something has to be in the tracker before a run can be refused for anything
# else, so the first work item is filed here rather than where the README files
# one, which is inside its third step.
bd create --title="Add a subtract function" \
  --description="calc has add and nothing else. Add subtract(a, b) with a test." \
  --type=feature --priority=2 >/dev/null 2>&1
item="$(bd ready --json 2>/dev/null | python3 -c '
import json, sys
payload = json.load(sys.stdin)
issues = payload if isinstance(payload, list) else payload.get("issues", payload)
print(issues[0]["id"])
')"
printf 'work item: %s\n' "$item"
refusal="$("$yoyo" run "$item" 2>&1 || true)"
contains "$refusal" "requires at least one configured check" "yoyo run refuses a run with no checks"

step "6. choose one of the candidates init offered"
# The README says choosing costs one character: open the empty list, then delete
# the leading "#" from the candidate that belongs. That is done here exactly as
# written rather than by writing the line out again, because the claim being
# checked is that the generated file can be edited that way.
chosen="python3 -m unittest discover -q -s tests -t ."
# The edit runs as an `if` condition so a candidate that was never offered is
# reported as the failed claim it is, rather than aborting the walk under set -e.
if python3 - "$chosen" <<'PY'
import pathlib
import sys

chosen = sys.argv[1]
path = pathlib.Path(".yoyodyne/config.yaml")
text = path.read_text()
if "#  - " + chosen + "\n" not in text:
    raise SystemExit("init offered no candidate for %r" % chosen)
text = text.replace("checks: []\n", "checks:\n", 1)
text = text.replace("#  - " + chosen + "\n", "  - " + chosen + "\n", 1)
path.write_text(text)
PY
then
  pass "uncommented the chosen candidate in place"
else
  fail "could not uncomment the chosen candidate"
fi
# `|| true` so a command that fails is reported as the claim it broke rather than
# aborting the walk under set -e with nothing said about which step it was.
effective="$("$yoyo" config show --effective 2>&1 || true)"
contains "$effective" "$chosen" "the uncommented candidate is the effective checks list"
# The README says each entry runs through /bin/sh -c and must exit non-zero on
# failure, so the declared command is executed exactly that way.
if /bin/sh -c "$chosen" >/dev/null 2>&1; then
  pass "the chosen check passes through /bin/sh -c"
else
  fail "the chosen check does not pass through /bin/sh -c"
fi
validate="$("$yoyo" config validate 2>&1)"
contains "$validate" "configuration valid" "config validate passes with the project's own checks"
mkdir -p src/nested
validate="$(cd src/nested && "$yoyo" config validate 2>&1)"
contains "$validate" "configuration valid" "configuration is discovered from a subdirectory"
rm -rf src

step "7. doctor checks the whole installation, not only the file"
# The README pairs `config validate` with `yoyo doctor` here: one is about a
# document loading, the other about whether work can actually run. What is
# asserted is the promise doctor makes -- every finding that is not healthy
# carries a command -- and that the parts this walk has just set up are the
# parts it calls healthy. It is not asserted to pass overall: this scratch
# project runs a `yoyo` that is deliberately not on PATH, and may run without an
# authenticated provider, both of which doctor is right to report.
# `--json` is machine-readable output, so it is read from stdout alone. Folding
# stderr into it would turn one stray diagnostic line into a parse failure
# reported as a broken report, which is a different claim from the one being
# checked -- and "this output is machine-readable" is itself worth asserting.
doctor_stderr="$scratch/doctor.stderr"
diagnosis="$("$yoyo" doctor --json 2>"$doctor_stderr" || true)"
if [ -s "$doctor_stderr" ]; then
  fail "doctor --json wrote to stderr, so its output is not machine-readable -- got: $(cat "$doctor_stderr")"
elif reason="$(python3 - "$diagnosis" <<'ASSERT' 2>&1
import json, sys

report = json.loads(sys.argv[1])
if report.get("schema_version") != 1:
    raise SystemExit("doctor --json reported schema_version %r" % report.get("schema_version"))
findings = {finding["check"]: finding for finding in report["findings"]}
# These four are what the walk has itself set up by now, so they are the ones it
# can hold doctor to. `path` and `binary` are deliberately not among them: this
# walk runs a `yoyo` that was never put on PATH, which doctor is right to report.
for check in ("configuration", "repository", "tracker", "checks"):
    if check not in findings:
        raise SystemExit("doctor never checked %s" % check)
    if findings[check]["status"] != "ok":
        raise SystemExit("doctor reports %s as %s: %s" % (check, findings[check]["status"], findings[check]["summary"]))
for finding in report["findings"]:
    if finding["status"] != "ok" and not finding.get("remedy", "").strip():
        raise SystemExit("doctor reported %s as %s with no remedy" % (finding["check"], finding["status"]))
ASSERT
)"; then
  pass "doctor calls this project's configuration, repository, tracker, and checks healthy, and carries a remedy for everything it does not"
else
  # The assertion says which claim broke, so this reports that rather than
  # summarizing every failure as the same sentence.
  fail "$reason -- report was: $diagnosis"
fi

step "7b. yoyo setup reaches the same state by asking, and again changes nothing"
# The README offers `yoyo setup` as steps 2 to 7 above, walked as questions. Two
# claims are worth executing rather than asserting: that it actually converges a
# project with nothing in it, and that running it a second time changes nothing
# -- which is also the resumability claim, since setup keeps no record of its own
# and a resumed walk is just a second one.
#
# It runs against its own scratch project rather than the one above, because a
# project that is already configured could not show the first claim. `--yes`
# answers every question with the answer setup proposes, which declines the
# optional Slack tier and so needs no workspace and no tokens.
asked="$scratch/asked"
mkdir -p "$asked"
( cd "$asked" \
  && git init -q -b main . \
  && git config user.email walk@example.invalid \
  && git config user.name "Adoption Walk" \
  && git remote add origin "$scratch/origin.git" \
  && printf 'check:\n\techo ok\n' > Makefile \
  && git add -A && git commit -qm "a project with a Makefile" )
setup_stderr="$scratch/setup.stderr"
# It exits nonzero here for the same reason doctor does above -- this walk runs a
# `yoyo` that was never put on PATH -- so the report is what is read, not the
# status.
walked="$("$yoyo" setup --directory "$asked" --yes --json 2>"$setup_stderr" || true)"
if [ -s "$setup_stderr" ]; then
  fail "setup --json wrote to stderr, so its output is not machine-readable -- got: $(cat "$setup_stderr")"
elif reason="$(python3 - "$walked" <<'ASSERT' 2>&1
import json, sys

report = json.loads(sys.argv[1])
if report.get("schema_version") != 1:
    raise SystemExit("setup --json reported schema_version %r" % report.get("schema_version"))
steps = {step["step"]: step for step in report["steps"]}
for name in ("tracker", "configuration", "tracker-remote"):
    if name not in steps:
        raise SystemExit("setup never reached the %s step" % name)
    # The tracker's sync remote is the one step recent bd versions may have
    # settled themselves at `bd init`, which setup reports as already true
    # rather than doing again; both outcomes are the README's claim.
    settled = ("done",) if name != "tracker-remote" else ("done", "already")
    if steps[name]["status"] not in settled:
        raise SystemExit("setup reports %s as %s: %s" % (name, steps[name]["status"], steps[name]["summary"]))
# Every step it did not finish has to name a command, which is the promise it
# shares with doctor: a step that says what is undone and not what to do about
# it is what this path exists to end.
for step in report["steps"]:
    if step["status"] in ("already", "done"):
        continue
    if not step.get("remedy", "").strip():
        raise SystemExit("setup left %s %s with nothing to run" % (step["step"], step["status"]))
findings = {finding["check"]: finding for finding in report["diagnosis"]["findings"]}
for check in ("configuration", "repository", "tracker", "checks"):
    if findings.get(check, {}).get("status") != "ok":
        raise SystemExit("after setup, doctor reports %s as %r" % (check, findings.get(check, {}).get("status")))
ASSERT
)"; then
  pass "setup converges a blank project, and doctor then calls its configuration, repository, tracker, and checks healthy"
else
  fail "$reason -- report was: $walked"
fi

if [ -f "$asked/.yoyodyne/config.yaml" ]; then
  before="$(cksum < "$asked/.yoyodyne/config.yaml")"
  again="$("$yoyo" setup --directory "$asked" --yes --json 2>/dev/null || true)"
  after="$(cksum < "$asked/.yoyodyne/config.yaml")"
  if [ "$before" != "$after" ]; then
    fail "a second setup rewrote a configuration that was already there"
  elif reason="$(python3 - "$again" <<'ASSERT' 2>&1
import json, sys

steps = {step["step"]: step for step in json.loads(sys.argv[1])["steps"]}
for name in ("tracker", "configuration", "tracker-remote"):
    if steps.get(name, {}).get("status") != "already":
        raise SystemExit("a second setup reports %s as %r, not as already true" % (name, steps.get(name, {}).get("status")))
ASSERT
)"; then
    pass "running setup again reports what is already true and changes nothing"
  else
    fail "$reason -- report was: $again"
  fi
else
  fail "setup wrote no configuration to run again over"
fi

step "8. write down what the product is for"
mkdir -p docs/product
cat > docs/product/calc.md <<'MD'
# Calc

A tiny arithmetic library, kept small enough that a change to it is obvious.

## Goals

- Arithmetic is correct for the operations the library claims to support.
- Every operation has a test that would fail if the operation broke.
MD
pass "wrote a specification with an introduction and goals"

step "9. a run refuses an uncommitted primary checkout, and names the files"
refusal="$("$yoyo" run "$item" 2>&1 || true)"
contains "$refusal" "uncommitted changes" "the run refuses an uncommitted primary checkout"
contains "$refusal" ".yoyodyne/config.yaml" "the refusal names the file that is dirty"
contains "$refusal" "docs/product/calc.md" "the refusal names every file that is dirty"

# The README also says the tracker's own exports are excepted from that refusal.
# Reaching that case through `yoyo run` would put a developer agent on the item,
# so it is verified where it is enforced instead.
( cd "$repository" && run go test ./internal/gitworktree \
  -run TestManagerAllowsOnlyConfiguredPrimaryControlPlaneChanges -count=1 )
pass ".beads/issues.jsonl and .beads/interactions.jsonl are excepted from that refusal"

git add -A
git commit -qm "adopt yoyo"
pass "committed the adoption"

step "10. the commands the README points a new project at"
reconcile="$("$yoyo" reconcile 2>&1)"
contains "$reconcile" "no runs need reconciliation" "yoyo reconcile reports nothing outstanding"
invariants="$("$yoyo" invariant list 2>&1)"
contains "$invariants" "no invariants are recorded" "a project with no invariants directory simply has none"
origins="$("$yoyo" config show --origins 2>&1)"
contains "$origins" "$project/.yoyodyne/config.yaml" "config show --origins names the project file"
missing "$origins" "builtin:v1" "nothing is inherited from the built-in bundle"

step "11. following a run or a conversation"
# `yoyo status --list` resolves the state directory the same way the harness
# does, which is what makes the temporary state root above enough to keep this
# off an operator's real runs. Both spellings are exercised. What it reports
# about runs, conversations, branch reviews, and exchanges is checked by the Go
# tests in internal/cli and internal/runstate, which need no provider and no
# repository to build all four.
# What is asserted is the root and the product, not the layout between them:
# where one product's directory sits inside the state root is the harness's to
# arrange, and a walk that spelled it out went red on the change that moved it
# (yoyodyne-8sy) rather than on anything the README says.
status_listing="$("$yoyo" status --list 2>&1 || true)"
case "$status_listing" in
  (*"recorded under $scratch/state/"*calc*)
    pass "yoyo status --list reads the state root this walk's runs recorded under" ;;
  (*)
    fail "yoyo status --list reads the state root this walk's runs recorded under -- got: $status_listing" ;;
esac
# The runs above recorded this walk's root in the checkout's state-root marker,
# so a second root is refused rather than listed: one product's state is never
# split across two roots. Which root each spelling resolves is read from `yoyo
# config show`, which records no marker, and the refusal is checked to name it.
shown="$(YOYODYNE_STATE_HOME="$scratch/named" "$yoyo" config show 2>&1 || true)"
contains "$shown" "# state root: $scratch/named (from environment:YOYODYNE_STATE_HOME)" \
  "the state root honors YOYODYNE_STATE_HOME"
shown="$(env -u YOYODYNE_STATE_HOME XDG_STATE_HOME="$scratch/xdg" \
  "$yoyo" config show 2>&1 || true)"
contains "$shown" "# state root: $scratch/xdg/yoyodyne (from environment:XDG_STATE_HOME)" \
  "the state root honors XDG_STATE_HOME by appending yoyodyne"
status_listing="$(YOYODYNE_STATE_HOME="$scratch/named" \
  "$yoyo" status --list 2>&1 || true)"
contains "$status_listing" "one product's state is never split across two roots" \
  "yoyo status --list refuses a root the checkout's marker disagrees with"
contains "$status_listing" "resolved $scratch/named (from environment:YOYODYNE_STATE_HOME)" \
  "the refusal names the root it resolved and the setting that chose it"
if [ -e "$scratch/named" ]; then
  fail "the refused process wrote nothing under the root it resolved -- got: $scratch/named"
else
  pass "the refused process wrote nothing under the root it resolved"
fi
# The spend report needs nothing installed beside the binary, which is half of
# why it stopped being a shell script.
spend="$("$yoyo" status --spend 2>&1 || true)"
contains "$spend" "recorded under" "yoyo status --spend reports on a machine with nothing recorded"

step "12. drive it from the conversation"
if [ "${WALK_PROVIDER:-0}" = "1" ]; then
  # Everything up to here is free. This is the step that spends capacity, so it
  # only runs when it was asked for. What is asserted is that the harness got
  # all the way to the provider: no configuration, tracker, or repository
  # refusal stood between the documented steps and a developer being asked.
  outcome="$("$yoyo" run "$item" 2>&1 || true)"
  printf '%s\n' "$outcome"
  missing "$outcome" "requires at least one configured check" "the run got past the checks gate"
  missing "$outcome" "uncommitted changes" "the run got past the repository readiness gate"
  contains "$outcome" "run-" "a run was created and reported an outcome by id"

  # There is a run to look at now, so the verb the README points at for watching
  # one is exercised against a real record rather than an empty directory.
  listing="$("$yoyo" status --list 2>&1 || true)"
  printf '%s\n' "$listing"
  contains "$listing" "run-" "yoyo status --list lists the run"
  cost="$("$yoyo" status --spend 2>&1 || true)"
  printf '%s\n' "$cost"
  case "$cost" in
    (*"USD"*|*"TOTAL"*|*"no completed provider invocations"*)
      pass "yoyo status --spend reports what the run spent" ;;
    (*)
      fail "yoyo status --spend produced no report -- got: $cost" ;;
  esac

  chat="$("$yoyo" chat --message "What should we do first?" 2>&1 || true)"
  printf '%s\n' "$chat"
  contains "$chat" "context gathered" "the conversation opened and gathered its picture"
else
  printf 'skipped: set WALK_PROVIDER=1 to invoke the provider on this step.\n'
  printf 'Everything before it ran without one.\n'
fi

printf '\n=== result\n'
if [ "$failures" = "0" ]; then
  printf 'the documented adoption path works as written\n'
else
  printf '%d documented claim(s) did not hold\n' "$failures"
fi
if [ "$skips" != "0" ]; then
  printf '%d claim(s) this environment could not exercise, named above\n' "$skips"
fi
exit "$failures"
