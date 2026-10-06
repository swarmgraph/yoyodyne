#!/usr/bin/env bash
#
# install.sh - put `yoyo` on this machine's PATH and name what it still needs.
#
#   curl -fsSL https://raw.githubusercontent.com/swarmgraph/yoyodyne/main/scripts/install.sh | bash
#   scripts/install.sh --dir ~/bin --version v0.3.0
#
# This is the README's install section executed rather than read: detect the
# platform, download the release binary for it and check it against the
# release's own checksums (or `go install` where no release binary exists for
# this platform), put it in one directory, name the shell-profile line when
# that directory is not already on PATH, and check the two hard prerequisites
# -- bd and claude -- naming whichever one is missing, or installing claude
# where --install-prereqs asked for it.
#
# It is a script rather than part of the binary for the one reason that
# survives scrutiny: it is the part that fetches the binary, so it runs on a
# machine that does not have it yet. Everything after "yoyo is on PATH" --
# `yoyo setup`, `yoyo doctor` -- is written in Go, because by then there is a
# Go binary to write it in.
#
# What it will not do is edit a shell profile, use sudo, or touch anything in a
# project. Left to itself it writes exactly one file, the binary; every other
# change to this machine is printed as a line for a person to run, including
# the installs for a missing prerequisite. --install-prereqs is how somebody
# asks for one of those to be run rather than printed, and it is the only way
# this writes anything else. A script fetched over the network that installs
# what it likes, or asks for a root password, is a habit nobody should be
# taught.
#
# Requires bash, curl, tar, and one of shasum or sha256sum. A platform with no
# release binary needs Go 1.25 or newer instead of curl and tar.

set -euo pipefail

# The repository's home and its Go module path differ: the repository moved to
# swarmgraph, and the module path stayed where go.mod declares it, which
# `go install` reaches through GitHub's redirect.
repository_slug="swarmgraph/yoyodyne"
repository_url="https://github.com/$repository_slug"
module_path="github.com/mason-bryant/yoyodyne"
beads_url="https://github.com/gastownhall/beads"
claude_url="https://code.claude.com/docs"

# The platforms a release ships a prebuilt binary for, spelled goos/goarch.
# This is the Makefile's PLATFORMS, and scripts/install-test.sh asserts the two
# still agree -- otherwise a platform dropped from a release is discovered by
# whoever runs this on it, as a 404 with nothing installed.
released_platforms="darwin/arm64 darwin/amd64 linux/amd64"

step()  { printf '\n=== %s\n' "$*"; }
say()   { printf '  %s\n' "$*"; }
run()   { printf '  $ %s\n' "$*"; "$@"; }
# Every refusal says what was not installed, because the next question is
# always whether this machine was left half-changed. Nothing was: the binary is
# the last thing written, and it is written whole or not at all.
refuse() { printf '\ninstall.sh: %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<USAGE
install.sh - install yoyo and check what it needs

  --version <tag>     install this release rather than the newest one
  --dir <path>        install into this directory rather than the default
  --from-source       build with \`go install\` rather than downloading a release
  --install-prereqs   install a missing claude rather than only naming it
  --help              print this

bd and claude are always checked, and whichever is missing is always named.
Nothing but the yoyo binary is installed unless --install-prereqs asks for it.

Environment: YOYO_VERSION, YOYO_INSTALL_DIR are the same as --version and --dir.
USAGE
}

tag="${YOYO_VERSION:-}"
install_dir="${YOYO_INSTALL_DIR:-}"
from_source=0
install_prereqs=0

while [ $# -gt 0 ]; do
  case "$1" in
    --version) [ $# -ge 2 ] || refuse "--version needs a tag"; tag="$2"; shift 2 ;;
    --dir)     [ $# -ge 2 ] || refuse "--dir needs a path";    install_dir="$2"; shift 2 ;;
    --from-source)     from_source=1; shift ;;
    --install-prereqs) install_prereqs=1; shift ;;
    --help|-h)      usage; exit 0 ;;
    *) usage >&2; refuse "unknown argument: $1" ;;
  esac
done

scratch="$(mktemp -d "${TMPDIR:-/tmp}/yoyo-install.XXXXXX")"
trap 'rm -rf "$scratch"' EXIT

need() { command -v "$1" >/dev/null 2>&1; }

# One spelling of each fetch, so scripts/install-test.sh can stand a stub in
# front of curl and see the URLs this actually asks for.
fetch_stdout()  { curl -fsSL "$1"; }
fetch_to_file() { curl -fsSL -o "$2" "$1"; }

sha256_of() {
  if need shasum; then shasum -a 256 "$1" | awk '{print $1}'
  else sha256sum "$1" | awk '{print $1}'
  fi
}

# Go's own version, as two numbers. A toolchain older than the go directive in
# go.mod cannot build this module, and finding that out as a compiler error
# three screens long is worse than being told.
go_is_new_enough() {
  local reported major minor rest
  reported="$(go version 2>/dev/null | awk '{print $3}')"
  reported="${reported#go}"
  major="${reported%%.*}"
  rest="${reported#*.}"
  minor="${rest%%.*}"
  minor="${minor%%[!0-9]*}"
  case "$major" in (''|*[!0-9]*) return 1 ;; esac
  case "$minor" in (''|*[!0-9]*) minor=0 ;; esac
  [ "$major" -gt 1 ] && return 0
  [ "$major" -eq 1 ] && [ "$minor" -ge 25 ]
}

step "this machine"

case "$(uname -s)" in
  Darwin) goos=darwin ;;
  Linux)  goos=linux ;;
  *) refuse "$(uname -s) is not a platform yoyo runs on. macOS is where it is developed and used, Linux is built and exercised by CI, and there is no Windows binary" ;;
esac
case "$(uname -m)" in
  arm64|aarch64) goarch=arm64 ;;
  x86_64|amd64)  goarch=amd64 ;;
  *) refuse "$(uname -m) is not an architecture yoyo is built for" ;;
esac
platform="$goos/$goarch"
say "platform: $platform"

has_release_binary=0
for candidate in $released_platforms; do
  [ "$candidate" = "$platform" ] && has_release_binary=1
done

if [ "$from_source" = "1" ]; then
  route=source
elif [ "$has_release_binary" = "1" ]; then
  route=release
elif need go && go_is_new_enough; then
  # A platform with no prebuilt binary is not a platform yoyo refuses; it is one
  # nobody has built for it yet, and Go will. Say which of the two happened, so
  # a slow first run is explained rather than mysterious.
  say "no release binary is published for $platform, so this builds one"
  route=source
else
  refuse "no release binary is published for $platform and Go 1.25 or newer is not installed, so there is nothing here to install from. Install Go from https://go.dev/dl and run this again, or build from a checkout: $repository_url"
fi

# Where the binary goes. /usr/local/bin is preferred because it is already on
# almost every PATH, and an install nobody has to add a PATH line for is an
# install that cannot half-work -- which is the exact failure this script
# exists to end. It is used only when it is writable as this user, since
# nothing here escalates.
if [ -z "$install_dir" ]; then
  if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    install_dir=/usr/local/bin
  else
    install_dir="$HOME/.local/bin"
  fi
fi
say "install directory: $install_dir"

mkdir -p "$install_dir" || refuse "$install_dir could not be created. Pass a directory you can write: --dir ~/bin"
[ -w "$install_dir" ] || refuse "$install_dir is not writable by this user. Pass a directory you can write: --dir ~/bin"

if [ "$route" = "release" ]; then
  step "download the release binary"
  for tool in curl tar; do
    need "$tool" || refuse "a release download needs $tool, which is not installed. Install it, or pass --from-source to build with Go instead"
  done
  need shasum || need sha256sum ||
    refuse "a release download needs shasum or sha256sum to check what it downloaded, and this machine has neither. Install one, or pass --from-source to build with Go instead"

  if [ -z "$tag" ]; then
    say "asking which release is newest"
    tag="$(fetch_stdout "https://api.github.com/repos/$repository_slug/releases/latest" 2>/dev/null |
      sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -1)" || true
    [ -n "$tag" ] || refuse "could not find out which release is newest. Pick one from $repository_url/releases and pass it: --version v0.3.0"
  fi
  say "release: $tag"

  archive="yoyo_${tag}_${goos}_${goarch}.tar.gz"
  base="$repository_url/releases/download/$tag"
  fetch_to_file "$base/$archive" "$scratch/$archive" ||
    refuse "$base/$archive could not be downloaded. Check that $tag is a release at $repository_url/releases"
  fetch_to_file "$base/checksums.txt" "$scratch/checksums.txt" ||
    refuse "$tag publishes no checksums.txt, so the download could not be checked. Nothing was installed"

  # The checksum is checked here rather than trusted, and a mismatch refuses
  # rather than warns: a binary that is not the one the release published is
  # not a binary this script has anything to say about.
  expected="$(awk -v name="$archive" '$2 == name || $2 == "*" name {print $1}' "$scratch/checksums.txt" | head -1)"
  [ -n "$expected" ] ||
    refuse "$tag's checksums.txt does not list $archive, so the download could not be checked. Nothing was installed"
  actual="$(sha256_of "$scratch/$archive")"
  [ "$expected" = "$actual" ] ||
    refuse "$archive does not match the checksum $tag published ($actual, expected $expected). Nothing was installed"
  say "checksum matches what $tag published"

  tar -xzf "$scratch/$archive" -C "$scratch" || refuse "$archive could not be unpacked"
  [ -f "$scratch/yoyo" ] || refuse "$archive did not contain a yoyo binary"
  install -m 0755 "$scratch/yoyo" "$install_dir/yoyo" || refuse "$install_dir/yoyo could not be written"
else
  step "build with go install"
  need go || refuse "--from-source needs Go 1.25 or newer, which is not installed. Get it from https://go.dev/dl"
  go_is_new_enough || refuse "yoyo needs Go 1.25 or newer; this machine has $(go version | awk '{print $3}')"
  # GOBIN rather than go's default, so this script has one install directory to
  # report and one PATH line to name, whichever route it took.
  run env GOBIN="$install_dir" go install "$module_path/cmd/yoyo@${tag:-latest}" ||
    refuse "go install $module_path/cmd/yoyo@${tag:-latest} failed. Nothing was installed"
fi

step "check the binary runs"
version="$("$install_dir/yoyo" version 2>&1)" ||
  refuse "$install_dir/yoyo was installed but does not run: $version"
say "$install_dir/yoyo reports $version"

step "PATH"
on_path=0
case ":$PATH:" in (*":$install_dir:"*) on_path=1 ;; esac
if [ "$on_path" = "1" ]; then
  resolved="$(command -v yoyo || true)"
  if [ "$resolved" = "$install_dir/yoyo" ]; then
    say "$install_dir is on your PATH, so \`yoyo\` is this binary"
  else
    # An earlier PATH entry holding another yoyo is the one failure here that
    # looks exactly like success: the version this prints would be the other
    # binary's, and every later confusion traces back to this line.
    say "$install_dir is on your PATH, but \`yoyo\` resolves to ${resolved:-nothing} first."
    say "Remove that one, or put $install_dir earlier on your PATH."
  fi
else
  case "${SHELL:-}" in
    */fish) profile="$HOME/.config/fish/config.fish"; path_line="fish_add_path $install_dir" ;;
    */bash) profile="$HOME/.bashrc"; path_line="echo 'export PATH=\"\$PATH:$install_dir\"' >> $profile" ;;
    */zsh)  profile="$HOME/.zshrc";  path_line="echo 'export PATH=\"\$PATH:$install_dir\"' >> $profile" ;;
    *)      profile="your shell's startup file"; path_line="export PATH=\"\$PATH:$install_dir\"" ;;
  esac
  say "$install_dir is not on your PATH, so \`yoyo\` will not be found yet."
  say "Add it once, in the shell you use ($profile):"
  printf '\n      %s\n\n' "$path_line"
  say "Then open a new shell, or run: export PATH=\"\$PATH:$install_dir\""
fi

step "prerequisites"

missing=""
# Both of these are required rather than optional: bd is the tracker every role
# reads and writes, and claude executes every agent role. A prerequisite this
# script cannot install is named with where to get it, which is the honest half
# of "installed or named" -- a machine is never told a tool is there when it is
# not.
report_prerequisite() {
  local tool="$1" what="$2" where="$3"
  if need "$tool"; then
    say "$tool: $(command -v "$tool")"
    return 0
  fi
  missing="$missing $tool"
  say "$tool is not installed. $what: $where"
  return 1
}

# bd is named rather than installed, in every case. No install route for it is
# one this repository can vouch for, and a guessed module path installs the
# wrong thing or nothing at all -- both worse than the URL of its home.
report_prerequisite bd "the tracker every role reads and writes" "$beads_url" || true

if ! report_prerequisite claude "what executes every agent role" "$claude_url"; then
  if [ "$install_prereqs" = "1" ]; then
    # Only ever reached because it was asked for. Installing it uninvited would
    # mean a script fetched over the network running a second one unattended,
    # which is not a thing to do to somebody's machine on their behalf.
    say "installing it, because --install-prereqs was passed:"
    if need npm; then
      run npm install -g @anthropic-ai/claude-code || true
    else
      run bash -c 'curl -fsSL https://claude.ai/install.sh | bash' || true
    fi
    # An installer that put it somewhere not yet on this shell's PATH has still
    # done its job, so the recheck asks the same question rather than assuming.
    if need claude; then
      say "claude: $(command -v claude)"
      missing="${missing/ claude/}"
    else
      say "claude is still not on this shell's PATH. Open a new shell, or install it from $claude_url"
    fi
  else
    say "Or pass --install-prereqs and this script will install it for you."
  fi
fi

step "what to do next"
say "yoyo $version is at $install_dir/yoyo"
if [ -n "$missing" ]; then
  say "Still needed before a run:$missing"
fi
# What the binary just installed can actually do, asked rather than assumed.
# This script can be installed from a release older than the commands it would
# otherwise recommend, and the last thing a first run says is the worst place
# to name a command that does not exist. Where a verb is absent the line that
# needed it is left out rather than softened.
help_text="$("$install_dir/yoyo" help 2>&1 || true)"
installed_yoyo_has() {
  printf '%s\n' "$help_text" | grep -qE "^[[:space:]]+$1([[:space:]]|\$)"
}

if installed_yoyo_has doctor; then
  say "claude must also be authenticated; \`yoyo doctor\` in your project says whether it is."
else
  say "claude must also be authenticated before a run can use it: $claude_url"
fi
# The three steps the README's own getting-started spine names, in its order and
# its spelling. A machine that followed this script and a reader who followed
# the README have to end up in the same place, or one of the two is a path
# nobody has walked.
say "Then, in your own project:"
printf '\n      cd path/to/your/project\n      bd init && yoyo init\n      yoyo chat\n\n'
if installed_yoyo_has setup; then
  say "\`yoyo setup\` walks the middle step and everything around it as questions,"
  say "if you would rather be asked than type."
fi
